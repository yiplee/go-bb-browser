package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthGet(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
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
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body: %#v", body)
	}
}

func TestHealthPostMethodNotAllowed(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
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
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
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
