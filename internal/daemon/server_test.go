package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestHealthGet(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
		StateDir:    stateDirDisabled,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SkipBrowserAttach = true
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body protocol.HealthResult
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" {
		t.Fatalf("status field: %#v", body)
	}
	if body.Browser != protocol.HealthBrowserSkipped {
		t.Fatalf("browser field: %#v", body)
	}
}

func TestHealthBrowserConnected(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
		StateDir:    stateDirDisabled,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.tabHook = &fakeConn{infos: []*target.Info{{TargetID: "T1", Type: "page"}}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body protocol.HealthResult
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Browser != protocol.HealthBrowserConnected {
		t.Fatalf("browser field: %#v", body)
	}
}

func TestHealthBrowserDisconnected(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
		StateDir:    stateDirDisabled,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.tabHook = &fakeConn{pageErr: fmt.Errorf("cdp down")}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body protocol.HealthResult
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Browser != protocol.HealthBrowserDisconnected {
		t.Fatalf("browser field: %#v", body)
	}
}

func TestLiveReadyAndHealthUseCachedSupervisorState(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: stateDirDisabled}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeConn{infos: []*target.Info{{TargetID: "T1", Type: "page"}}}
	srv.tabLive = fc
	srv.browserState = sessionSuspect
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for path, want := range map[string]int{
		"/live":   http.StatusOK,
		"/health": http.StatusOK,
		"/ready":  http.StatusServiceUnavailable,
	} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s status = %d, want %d", path, res.StatusCode, want)
		}
	}
	if got := fc.pageCalls.Load(); got != 0 {
		t.Fatalf("health routes enumerated targets %d times", got)
	}
	if got := fc.pingCalls.Load(); got != 0 {
		t.Fatalf("health routes pinged browser %d times", got)
	}
}

type deadlineHealthConn struct{ fakeConn }

func (*deadlineHealthConn) PingBrowserContext(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestHealthProbeHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got := pingTabConnContext(ctx, &deadlineHealthConn{}); got != protocol.HealthBrowserDisconnected {
		t.Fatalf("health result = %q, want disconnected", got)
	}
}

func TestHealthPostMethodNotAllowed(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
		StateDir:    stateDirDisabled,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/health", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", res.StatusCode)
	}
	if g, w := res.Header.Get("Allow"), http.MethodGet; g != w {
		t.Fatalf("Allow: got %q want %q", g, w)
	}
}

func TestListenAndServeShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  addr,
		StateDir:    stateDirDisabled,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SkipBrowserAttach = true

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.ListenAndServe(ctx)
	}()

	var res *http.Response
	for range 50 {
		res, err = http.Get("http://" + addr + "/health")
		if err == nil && res.StatusCode == http.StatusOK {
			res.Body.Close()
			break
		}
		if res != nil {
			res.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

// blockingTargets stalls PageTargetsContext until release, simulating a CDP call
// that outlives http.Server.Shutdown's budget.
type blockingTargets struct {
	*fakeConn
	entered chan struct{}
	release <-chan struct{}
}

func (b *blockingTargets) PageTargetsContext(ctx context.Context) ([]*target.Info, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.PageTargets()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestScheduleAuditAfterCloseDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: dir}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.store.Close(); err != nil {
			t.Error(err)
		}
	})

	srv.auditMu.Lock()
	srv.auditClosed = true
	close(srv.auditCh)
	srv.auditMu.Unlock()
	<-srv.auditDone

	ctx := contextWithAudit(context.Background(), &auditMeta{
		action: protocol.MethodTabList,
		body:   json.RawMessage(`{"jsonrpc":"2.0","method":"tab_list","id":1}`),
		at:     time.Now().UTC(),
	})
	srv.scheduleAudit(ctx, []byte(`{"jsonrpc":"2.0","id":1,"result":{"seq":1}}`))

	data, err := os.ReadFile(filepath.Join(dir, "rpc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"action":"tab_list"`)) {
		t.Fatalf("audit missing after close: %s", data)
	}
}

func TestShutdownWaitsForHandlerBeforeClosingAudit(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	dir := t.TempDir()
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: addr, StateDir: dir}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SkipBrowserAttach = true
	srv.shutdownTimeout = 40 * time.Millisecond
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	defer releaseHandler()
	entered := make(chan struct{})
	srv.tabLive = &blockingTargets{fakeConn: &fakeConn{}, entered: entered, release: release}
	sessionClosed := make(chan struct{})
	srv.sessDone = func() { close(sessionClosed) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- srv.ListenAndServe(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		res, getErr := http.Get("http://" + addr + "/live")
		if getErr == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	clientErr := make(chan error, 1)
	go func() {
		res, postErr := http.Post("http://"+addr+"/v1", "application/json", strings.NewReader(rpcReq(protocol.MethodTabList, map[string]any{}, 1)))
		if res != nil {
			res.Body.Close()
		}
		clientErr <- postErr
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not reach CDP")
	}

	cancel()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("shutdown returned while handler still in CDP: %v", err)
	default:
	}
	select {
	case <-sessionClosed:
		t.Fatal("session closed while handler still in CDP")
	default:
	}
	releaseHandler()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out waiting for handler")
	}
	select {
	case <-sessionClosed:
	default:
		t.Fatal("session not closed after handler drained")
	}
	if err := <-clientErr; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "rpc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"action":"tab_list"`)) {
		t.Fatalf("in-flight audit missing: %s", data)
	}
}
