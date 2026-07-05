package store

import (
	"encoding/json"
)

const (
	logStringMaxLen   = 200
	logStringHead     = 80
	logStringTail     = 80
	logStringEllipsis = "...."
)

// compactRPCLogBody shortens very long string values in JSON-RPC request params
// for audit logging. The original request is unchanged; only the stored log line
// is lossy. Parsing failures return body unchanged.
func compactRPCLogBody(body json.RawMessage) json.RawMessage {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Params) == 0 {
		return body
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return body
	}

	changed := false
	for k, v := range params {
		s, ok := jsonStringValue(v)
		if !ok {
			continue
		}
		compacted := truncateMiddle(s, logStringMaxLen, logStringHead, logStringTail)
		if compacted == s {
			continue
		}
		newV, err := json.Marshal(compacted)
		if err != nil {
			return body
		}
		params[k] = newV
		changed = true
	}
	if !changed {
		return body
	}

	newParams, err := json.Marshal(params)
	if err != nil {
		return body
	}
	req.Params = newParams

	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func jsonStringValue(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func truncateMiddle(s string, maxLen, head, tail int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if head+tail >= len(runes) {
		return s
	}
	return string(runes[:head]) + logStringEllipsis + string(runes[len(runes)-tail:])
}
