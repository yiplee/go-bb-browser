package store

import (
	"encoding/json"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

const maxObsEventData = 4 << 10 // 4 KiB

// SanitizeResponse redacts large binary or observation payloads in a JSON-RPC response.
func SanitizeResponse(action string, resp []byte) json.RawMessage {
	if len(resp) == 0 {
		return resp
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(resp, &env); err != nil {
		return resp
	}
	result, ok := env["result"]
	if !ok || len(result) == 0 || string(result) == "null" {
		return resp
	}

	var sanitized json.RawMessage
	switch action {
	case protocol.MethodScreenshot:
		sanitized = sanitizeScreenshotResult(result)
	case protocol.MethodFetch:
		sanitized = sanitizeFetchResult(result)
	case protocol.MethodNetwork, protocol.MethodConsole, protocol.MethodErrors:
		sanitized = sanitizeObsResult(result)
	default:
		return resp
	}
	if sanitized == nil {
		return resp
	}
	env["result"] = sanitized
	out, err := json.Marshal(env)
	if err != nil {
		return resp
	}
	return out
}

func sanitizeScreenshotResult(result json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil
	}
	data, _ := obj["data"].(string)
	obj["data"] = map[string]any{
		"_omitted": "base64 image",
		"bytes":    len(data),
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}

func sanitizeFetchResult(result json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil
	}
	inner, ok := obj["result"].(map[string]any)
	if !ok {
		return nil
	}
	if body, ok := inner["body"].(string); ok && len(body) > 0 {
		inner["body"] = map[string]any{
			"_omitted": "response body",
			"bytes":    len(body),
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}

func sanitizeObsResult(result json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil
	}
	events, ok := obj["events"].([]any)
	if !ok {
		return nil
	}
	changed := false
	for i, ev := range events {
		evMap, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		dataStr, ok := evMap["data"].(string)
		if !ok {
			if raw, ok := evMap["data"]; ok {
				if b, err := json.Marshal(raw); err == nil {
					dataStr = string(b)
				}
			}
		}
		if len(dataStr) <= maxObsEventData {
			continue
		}
		evMap["data"] = map[string]any{
			"_truncated": true,
			"bytes":      len(dataStr),
			"preview":    dataStr[:maxObsEventData],
		}
		events[i] = evMap
		changed = true
	}
	if !changed {
		return nil
	}
	obj["events"] = events
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}
