package browser

import (
	"context"
	"testing"
	"time"

	"github.com/yiplee/go-bb-browser/internal/state"
)

func TestForegroundProbeTimeout(t *testing.T) {
	if foregroundProbeTimeout != 3*time.Second {
		t.Fatalf("foregroundProbeTimeout = %v, want 3s", foregroundProbeTimeout)
	}
	if foregroundProbeTimeout >= DefaultOpTimeout {
		t.Fatalf("foreground probe timeout %v must be shorter than DefaultOpTimeout %v", foregroundProbeTimeout, DefaultOpTimeout)
	}
}

func TestDetectForegroundShortNilSession(t *testing.T) {
	var s *Session
	sh, ok := s.DetectForegroundShort(context.Background(), nil)
	if ok || sh != "" {
		t.Fatalf("got (%q, %v), want (\"\", false)", sh, ok)
	}
}

func TestDetectForegroundShortRespectsContext(t *testing.T) {
	s := &Session{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sh, ok := s.DetectForegroundShort(ctx, []state.TabSnapshot{{ShortID: "abc", TargetID: "TARGET1234567890"}})
	if ok || sh != "" {
		t.Fatalf("got (%q, %v), want (\"\", false)", sh, ok)
	}
}
