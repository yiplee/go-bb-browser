package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/target"
)

// ErrorKind separates transport failures from target-local and request-local
// failures. Only ErrorTransport is an immediate daemon-fatal condition.
type ErrorKind uint8

const (
	ErrorRequest ErrorKind = iota
	ErrorTimeout
	ErrorTargetGone
	ErrorTransport
)

type CDPError struct {
	Kind   ErrorKind
	Op     string
	Target target.ID
	Err    error
}

func (e *CDPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	where := e.Op
	if e.Target != "" {
		where += " target=" + string(e.Target)
	}
	if where == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %v", where, e.Err)
}

func (e *CDPError) Unwrap() error { return e.Err }

func wrapCDPError(op string, tid target.ID, err error) error {
	if err == nil {
		return nil
	}
	var typed *CDPError
	if errors.As(err, &typed) {
		return err
	}
	return &CDPError{Kind: ErrorKindOf(err), Op: op, Target: tid, Err: err}
}

// ErrorKindOf classifies typed and raw chromedp/CDP errors.
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorRequest
	}
	var typed *CDPError
	if errors.As(err, &typed) {
		return typed.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no target"),
		strings.Contains(msg, "target not found"),
		strings.Contains(msg, "target closed"),
		strings.Contains(msg, "page has been closed"),
		strings.Contains(msg, "no page"),
		strings.Contains(msg, "no session with given id"),
		strings.Contains(msg, "no such target"):
		return ErrorTargetGone
	case strings.Contains(msg, "eof"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "use of closed network connection"),
		strings.Contains(msg, "websocket"),
		strings.Contains(msg, "channel closed"),
		strings.Contains(msg, "could not dial"):
		return ErrorTransport
	default:
		return ErrorRequest
	}
}
