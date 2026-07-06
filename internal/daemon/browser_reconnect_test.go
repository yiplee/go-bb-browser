package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yiplee/go-bb-browser/internal/browser"
)

func TestIsReconnectableCDPErr(t *testing.T) {
	if !isReconnectableCDPErr(context.Canceled) {
		t.Fatal("context.Canceled")
	}
	if !isReconnectableCDPErr(context.DeadlineExceeded) {
		t.Fatal("context.DeadlineExceeded")
	}
	if !isReconnectableCDPErr(errors.New("read tcp: use of closed network connection")) {
		t.Fatal("closed conn")
	}
	if !isReconnectableCDPErr(errors.New("channel closed")) {
		t.Fatal("channel closed")
	}
	if isReconnectableCDPErr(errors.New("unknown tab id")) {
		t.Fatal("non-CDP should be false")
	}
}

func TestCdpHintMarksSessionStale(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: "-"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.lastBrowserOK = time.Now()

	_ = srv.cdpHint(context.DeadlineExceeded)
	srv.tabMu.RLock()
	stale := srv.lastBrowserOK.IsZero()
	srv.tabMu.RUnlock()
	if !stale {
		t.Fatal("expected lastBrowserOK cleared after reconnectable CDP error")
	}
}

func TestProbeBrowserSessionNotConnected(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0", StateDir: "-"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.probeBrowserSession(); !errors.Is(err, errBrowserNotConnected) {
		t.Fatalf("got %v want errBrowserNotConnected", err)
	}
}

func TestConnectOptionsOpTimeoutDefault(t *testing.T) {
	if browser.DefaultOpTimeout != 30*time.Second {
		t.Fatalf("DefaultOpTimeout = %v", browser.DefaultOpTimeout)
	}
}
