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
