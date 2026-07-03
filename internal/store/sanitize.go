package store

import (
	"encoding/json"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

const maxObsEventData = 4 << 10 // 4 KiB
const maxSnapshotText = 4 << 10

// SanitizeRequest redacts sensitive or large fields in a JSON-RPC request body for audit storage.
func SanitizeRequest(action string, rawBody json.RawMessage) json.RawMessage {
	if len(rawBody) == 0 {
		return rawBody
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &req); err != nil {
		return rawBody
	}
	params, ok := req["params"]
	if !ok || len(params) == 0 || string(params) == "null" {
		return rawBody
	}
	sanitized := sanitizeParams(action, params)
	if sanitized == nil {
		return rawBody
	}
	req["params"] = sanitized
	out, err := json.Marshal(req)
	if err != nil {
		return rawBody
	}
	return out
}

func sanitizeParams(action string, params json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(params, &obj); err != nil {
		return nil
	}
	changed := false
	switch action {
	case protocol.MethodEval:
		if script, ok := obj["script"].(string); ok && script != "" {
			obj["script"] = map[string]any{"_omitted": "script", "bytes": len(script)}
			changed = true
		}
	case protocol.MethodFill:
		if text, ok := obj["text"].(string); ok && text != "" {
			obj["text"] = map[string]any{"_omitted": "text", "bytes": len(text)}
			changed = true
		}
	case protocol.MethodFetch:
		if body, ok := obj["body"].(string); ok && body != "" {
			obj["body"] = map[string]any{"_omitted": "request body", "bytes": len(body)}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}

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
	case protocol.MethodSnapshot:
		sanitized = sanitizeSnapshotResult(result)
	case protocol.MethodEval:
		sanitized = sanitizeEvalResult(result)
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

func sanitizeSnapshotResult(result json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil
	}
	if text, ok := obj["text"].(string); ok && len(text) > maxSnapshotText {
		obj["text"] = map[string]any{
			"_truncated": true,
			"bytes":      len(text),
			"preview":    text[:maxSnapshotText],
		}
	}
	if refs, ok := obj["refs"].(map[string]any); ok && len(refs) > 0 {
		obj["refs"] = map[string]any{
			"_omitted": "refs map",
			"count":    len(refs),
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}

func sanitizeEvalResult(result json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil
	}
	if raw, ok := obj["result"]; ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		if len(b) > maxObsEventData {
			obj["result"] = map[string]any{
				"_truncated": true,
				"bytes":      len(b),
				"preview":    string(b[:maxObsEventData]),
			}
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
