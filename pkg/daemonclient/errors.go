package daemonclient

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

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
