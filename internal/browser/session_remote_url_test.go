package browser

import (
	"errors"
	"testing"
)

func TestRemoteAllocatorURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"127.0.0.1:9222", "http://127.0.0.1:9222"},
		{"  127.0.0.1:9222  ", "http://127.0.0.1:9222"},
		{"[::1]:9222", "http://[::1]:9222"},
		{"http://127.0.0.1:9222", "http://127.0.0.1:9222"},
		{"https://127.0.0.1:9222", "https://127.0.0.1:9222"},
		{"ws://127.0.0.1:9222/devtools/browser/x", "ws://127.0.0.1:9222/devtools/browser/x"},
	}
	for _, tt := range tests {
		if got := remoteAllocatorURL(tt.in); got != tt.want {
			t.Errorf("remoteAllocatorURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestConnectFailureHint(t *testing.T) {
	if got := connectFailureHint(errors.New("failed to modify wsURL: EOF")); got == "" {
		t.Fatal("expected hint for EOF")
	}
	if got := connectFailureHint(errors.New("some other error")); got != "" {
		t.Fatalf("unexpected hint: %q", got)
	}
}

func TestIsStaleTargetErr(t *testing.T) {
	stale := []string{
		"No page (-32601)",
		"No page for session (-32601)",
		"No target with given id found",
		"Target not found",
		"target closed",
		"No session with given id (-32001)",
	}
	for _, s := range stale {
		if !isStaleTargetErr(errors.New(s)) {
			t.Errorf("expected stale: %q", s)
		}
	}
	notStale := []string{
		"",
		"EOF",
		"context canceled",
		"navigation failed: net::ERR_NAME_NOT_RESOLVED",
	}
	for _, s := range notStale {
		var err error
		if s != "" {
			err = errors.New(s)
		}
		if isStaleTargetErr(err) {
			t.Errorf("expected not stale: %q", s)
		}
	}
}
