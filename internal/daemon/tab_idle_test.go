package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func newIdleTestServer(fc *fakeConn, timeout time.Duration) *Server {
	cfg := Config{
		DebuggerURL:    "127.0.0.1:9222",
		ListenAddr:     "127.0.0.1:0",
		TabIdleTimeout: timeout,
	}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	srv := NewServer(cfg, nil)
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

func TestConfigValidateTabIdleTimeoutNegative(t *testing.T) {
	cfg := Config{
		DebuggerURL:    "127.0.0.1:9222",
		TabIdleTimeout: -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative tab idle timeout")
	}
}
