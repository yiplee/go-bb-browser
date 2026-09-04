package daemonclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// ErrEmptyPool is returned by [NewPool] when no backends are given.
var ErrEmptyPool = errors.New("daemonclient: pool requires at least one client")

// ErrTabRequired is returned by [Pool] when a tab-scoped method is called
// without a tab id. A pool has no shared focus tab across daemons.
var ErrTabRequired = errors.New("daemonclient: pool method requires a tab id")

// UnknownTabError is returned when a tab id is not pinned to any backend in
// this [Pool] (never created via the pool, already closed, or [Pool.ForgetTab]).
type UnknownTabError struct {
	Tab string
}

func (e *UnknownTabError) Error() string {
	if e == nil {
		return "daemonclient pool: unknown tab <nil>"
	}
	return fmt.Sprintf("daemonclient pool: unknown tab %q (not created via this pool, or already closed)", e.Tab)
}

// AllFailedError is returned when every backend in a [Pool] failed for an
// unbound operation (health check, tab_new, tab_list, tab_focus, or Call
// without a pinned tab).
type AllFailedError struct {
	Op   string
	Errs []error
}

func (e *AllFailedError) Error() string {
	if e == nil {
		return "daemonclient pool: <nil>"
	}
	parts := make([]string, 0, len(e.Errs))
	for _, err := range e.Errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	op := e.Op
	if op == "" {
		op = "request"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("daemonclient pool: all backends failed for %s", op)
	}
	return fmt.Sprintf("daemonclient pool: all backends failed for %s: %s", op, strings.Join(parts, "; "))
}

func (e *AllFailedError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return e.Errs
}

// RPCError is a JSON-RPC 2.0 error returned in the response body (HTTP 200).
type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if e == nil {
		return "json-rpc: <nil>"
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// UnmarshalData decodes error.data into dst when present.
func (e *RPCError) UnmarshalData(dst *protocol.ErrData) error {
	if e == nil {
		return errors.New("nil RPCError")
	}
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return errors.New("no error data")
	}
	return json.Unmarshal(e.Data, dst)
}

func rpcErrorFrom(re *protocol.ResponseError) *RPCError {
	if re == nil {
		return nil
	}
	return &RPCError{
		Code:    re.Code,
		Message: re.Message,
		Data:    append(json.RawMessage(nil), re.Data...),
	}
}

// HTTPError is a non-2xx HTTP response from the daemon (e.g. wrong method on /v1).
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "http: <nil>"
	}
	if len(e.Body) > 200 {
		return fmt.Sprintf("http %d: %s…", e.StatusCode, e.Body[:200])
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}
