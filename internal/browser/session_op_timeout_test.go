package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionOpCtxDeadline(t *testing.T) {
	s := &Session{opTimeout: 50 * time.Millisecond}
	ctx, cancel := s.opCtx(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > s.opTimeout+time.Second {
		t.Fatalf("unexpected remaining %v", remaining)
	}

	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("got %v want DeadlineExceeded", ctx.Err())
	}
}

func TestSessionOpCtxDefaultTimeout(t *testing.T) {
	s := &Session{}
	ctx, cancel := s.opCtx(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > DefaultOpTimeout+time.Second {
		t.Fatalf("unexpected remaining %v", remaining)
	}
}
