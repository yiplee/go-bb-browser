package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

type deadlineHealthConn struct{ fakeConn }

func (deadlineHealthConn) PingBrowserContext(ctx context.Context) error {
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
