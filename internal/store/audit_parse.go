package store

import (
	"encoding/json"
)

// ParseRPCAuditSummary extracts tab, seq, and ok/error from a JSON-RPC response body.
func ParseRPCAuditSummary(resp []byte) (tab string, seq uint64, ok bool, errMsg string) {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		return "", 0, false, "invalid json-rpc response"
	}
	if env.Error != nil {
		msg := env.Error.Message
		if msg == "" {
			msg = "rpc error"
		}
		return "", 0, false, msg
	}
	ok = true
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return "", 0, true, ""
	}
	var r struct {
		Tab string `json:"tab"`
		Seq uint64 `json:"seq"`
	}
	if err := json.Unmarshal(env.Result, &r); err != nil {
		return "", 0, true, ""
	}
	return r.Tab, r.Seq, true, ""
}

// TabFromRequestBody reads optional "tab" from JSON-RPC request params.
func TabFromRequestBody(body json.RawMessage) string {
	var req struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Params) == 0 {
		return ""
	}
	var p struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return ""
	}
	return p.Tab
}
