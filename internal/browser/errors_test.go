package browser

import (
	"context"
	"errors"
	"testing"
)

func TestErrorKindClassification(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want ErrorKind
	}{
		"timeout":     {context.DeadlineExceeded, ErrorTimeout},
		"target gone": {errors.New("No target with given id"), ErrorTargetGone},
		"transport":   {errors.New("websocket: close 1006"), ErrorTransport},
		"request":     {errors.New("invalid selector"), ErrorRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ErrorKindOf(tc.err); got != tc.want {
				t.Fatalf("kind = %v, want %v", got, tc.want)
			}
		})
	}
}
