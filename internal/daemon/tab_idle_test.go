package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/store"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func newIdleTestServer(fc *fakeConn, timeout time.Duration) *Server {
	return newIdleTestServerWithState(fc, timeout, stateDirDisabled)
}

func newIdleTestServerWithState(fc *fakeConn, timeout time.Duration, stateDir string) *Server {
	cfg := Config{
		DebuggerURL:      "127.0.0.1:9222",
		ListenAddr:       "127.0.0.1:0",
		TabIdleTimeout:   timeout,
		StateDir:         stateDir,
		IdleStartupGrace: 30 * time.Millisecond,
	}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		panic(err)
	}
	srv.tabHook = fc
	return srv
}

func postRPC(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	return rec
}

func drainRPCLog(t *testing.T, srv *Server) {
	t.Helper()
	srv.auditWG.Wait()
}

func TestExternalTargetRemovalClearsAllDaemonState(t *testing.T) {
	srv := newIdleTestServer(&fakeConn{}, time.Minute)
	tid := target.ID("ABCDEF123456")
	short := srv.tabs.RegisterPageTarget(tid)
	srv.tabIdle.MarkManaged(tid)
	seq, err := srv.store.NextSeq()
	if err != nil {
		t.Fatal(err)
	}
	srv.obsStore.PushNetwork(tid, seq, json.RawMessage(`{"url":"https://example.com"}`))
	unlock, ok := srv.lockTab(context.Background(), short)
	if !ok {
		t.Fatal("lock failed")
	}
	unlock()

	srv.removeTargetState(tid)
	if _, ok := srv.tabs.Lookup(short); ok {
		t.Fatal("registry entry retained")
	}
	if _, ok := srv.tabIdle.Snapshot()[tid]; ok {
		t.Fatal("idle state retained")
	}
	if events, _, _ := srv.obsStore.QueryNetwork(tid, 0); len(events) != 0 {
		t.Fatalf("observation events retained: %#v", events)
	}
	srv.tabMuOps.Lock()
	_, lockRetained := srv.tabCDPLocks[short]
	srv.tabMuOps.Unlock()
	if lockRetained {
		t.Fatal("keyed tab lock retained")
	}
}

func TestTabIdlePreExistingTabNotClosed(t *testing.T) {
	fc := &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "t", URL: "https://ex"},
	}}
	srv := newIdleTestServer(fc, 50*time.Millisecond)

	postRPC(t, srv, rpcReq(protocol.MethodTabList, map[string]any{}, 1))
	time.Sleep(60 * time.Millisecond)
	srv.closeExpiredTabs(context.Background())

	if len(fc.infos) != 1 {
		t.Fatalf("pre-existing tab closed: %#v", fc.infos)
	}
}

func TestTabIdleClosesManagedTab(t *testing.T) {
	fc := &fakeConn{infos: []*target.Info{}}
	srv := newIdleTestServer(fc, 50*time.Millisecond)

	postRPC(t, srv, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	time.Sleep(60 * time.Millisecond)
	srv.closeExpiredTabs(context.Background())

	if len(fc.infos) != 0 {
		t.Fatalf("managed tab not closed: %#v", fc.infos)
	}
}

func TestTabIdleTouchRenews(t *testing.T) {
	fc := &fakeConn{infos: []*target.Info{}}
	srv := newIdleTestServer(fc, 50*time.Millisecond)

	rec := postRPC(t, srv, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	var env struct {
		Result protocol.TabNewResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	tab := env.Result.Tab

	time.Sleep(30 * time.Millisecond)
	postRPC(t, srv, rpcReq(protocol.MethodGoto, map[string]any{
		"tab": tab,
		"url": "https://example.com",
	}, 2))
	time.Sleep(30 * time.Millisecond)
	srv.closeExpiredTabs(context.Background())

	if len(fc.infos) != 1 {
		t.Fatalf("touched tab closed: %#v", fc.infos)
	}
}

func TestTabIdleDisabled(t *testing.T) {
	fc := &fakeConn{infos: []*target.Info{}}
	srv := newIdleTestServer(fc, 0)

	postRPC(t, srv, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	time.Sleep(60 * time.Millisecond)
	srv.closeExpiredTabs(context.Background())

	if len(fc.infos) != 1 {
		t.Fatalf("tab closed while disabled: %#v", fc.infos)
	}
}

func TestTabIdleRepeatedReconcileDoesNotRenew(t *testing.T) {
	stateDir := t.TempDir()
	timeout := 60 * time.Millisecond
	fc := &fakeConn{infos: []*target.Info{}}
	srv := newIdleTestServerWithState(fc, timeout, stateDir)

	postRPC(t, srv, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	if len(fc.infos) != 1 {
		t.Fatalf("tab_new: %#v", fc.infos)
	}

	// Simulate repeated target syncs (e.g. tab_list polling) while the tab sits
	// idle. Reconciliation must not reset the already-tracked idle timer.
	deadline := time.Now().Add(timeout + 40*time.Millisecond)
	for time.Now().Before(deadline) {
		srv.reconcileIdleFromLog(srv.syncTabsFromTargets(fc.infos))
		time.Sleep(15 * time.Millisecond)
	}

	srv.closeExpiredTabs(context.Background())
	if len(fc.infos) != 0 {
		t.Fatalf("idle tab kept alive by repeated reconcile: %#v", fc.infos)
	}
}

func TestConfigValidateTabIdleTimeoutNegative(t *testing.T) {
	cfg := Config{
		DebuggerURL:    "127.0.0.1:9222",
		TabIdleTimeout: -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative tab idle timeout")
	}
}

func TestTabIdleRestartRestoresManagedTab(t *testing.T) {
	stateDir := t.TempDir()
	timeout := 50 * time.Millisecond

	fcA := &fakeConn{infos: []*target.Info{}}
	srvA := newIdleTestServerWithState(fcA, timeout, stateDir)
	rec := postRPC(t, srvA, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	var env struct {
		Result protocol.TabNewResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(fcA.infos) != 1 {
		t.Fatalf("tab_new: %#v", fcA.infos)
	}
	drainRPCLog(t, srvA)
	logPath := filepath.Join(stateDir, store.RPCLogFile())
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected rpc.jsonl: %v", err)
	}
	if err := srvA.store.Close(); err != nil {
		t.Fatal(err)
	}

	fcB := &fakeConn{infos: []*target.Info{
		{
			TargetID: fcA.infos[0].TargetID,
			Type:     "page",
			Title:    "t",
			URL:      "https://ex",
		},
	}}
	srvB := newIdleTestServerWithState(fcB, timeout, stateDir)
	snaps := srvB.syncTabsFromTargets(fcB.infos)
	srvB.reconcileIdleFromLog(snaps)

	time.Sleep(60 * time.Millisecond)
	srvB.closeExpiredTabs(context.Background())
	if len(fcB.infos) != 0 {
		t.Fatalf("restored managed tab not closed after idle: %#v", fcB.infos)
	}
}

func TestTabIdleRestartGraceDelaysClose(t *testing.T) {
	stateDir := t.TempDir()
	timeout := 80 * time.Millisecond
	grace := 40 * time.Millisecond
	tabID := target.ID("ABCDEF999999")
	expiredAt := time.Now().Add(-timeout - time.Millisecond)

	logPath := filepath.Join(stateDir, store.RPCLogFile())
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(store.LogRecord{
		Action: protocol.MethodTabNew,
		Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"tab_new","params":{},"id":1}`),
		Tab:    "9999",
		OK:     true,
		Time:   expiredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	logData := append(line, '\n')
	if err := os.WriteFile(logPath, logData, 0o644); err != nil {
		t.Fatal(err)
	}

	fc := &fakeConn{infos: []*target.Info{
		{TargetID: tabID, Type: "page", Title: "t", URL: "https://ex"},
	}}
	cfg := Config{
		DebuggerURL:      "127.0.0.1:9222",
		ListenAddr:       "127.0.0.1:0",
		TabIdleTimeout:   timeout,
		IdleStartupGrace: grace,
		StateDir:         stateDir,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.tabHook = fc
	snaps := srv.syncTabsFromTargets(fc.infos)
	srv.reconcileIdleFromLog(snaps)

	srv.closeExpiredTabs(context.Background())
	if len(fc.infos) != 1 {
		t.Fatalf("tab closed during grace: %#v", fc.infos)
	}

	time.Sleep(grace + timeout/2)
	srv.closeExpiredTabs(context.Background())
	if len(fc.infos) != 0 {
		t.Fatalf("tab not closed after grace+timeout: %#v", fc.infos)
	}
}

func TestUnwritableStateDirErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits")
	}
	readOnlyDir := t.TempDir()
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Skip("cannot chmod read-only:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })

	cfg := Config{
		DebuggerURL:    "127.0.0.1:9222",
		ListenAddr:     "127.0.0.1:0",
		TabIdleTimeout: 50 * time.Millisecond,
		StateDir:       readOnlyDir,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewServer(cfg, nil); err == nil {
		t.Fatal("expected error for unwritable state dir")
	}
}

func TestSyncTabIdlePresenceKeepsLog(t *testing.T) {
	stateDir := t.TempDir()
	fc := &fakeConn{infos: []*target.Info{}}
	srv := newIdleTestServerWithState(fc, time.Minute, stateDir)
	postRPC(t, srv, rpcReq(protocol.MethodTabNew, map[string]any{}, 1))
	if len(fc.infos) != 1 {
		t.Fatal("expected tab")
	}
	drainRPCLog(t, srv)

	srv.syncTabIdlePresence(nil)

	logPath := filepath.Join(stateDir, store.RPCLogFile())
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("rpc log cleared by presence sync")
	}
}
