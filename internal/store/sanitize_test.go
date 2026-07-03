package store

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1", nil)
	r.RemoteAddr = "192.168.1.5:43210"
	if got := ClientIP(r); got != "192.168.1.5" {
		t.Fatalf("RemoteAddr: got %q", got)
	}

	r.Header.Set("X-Real-IP", "10.0.0.2")
	if got := ClientIP(r); got != "10.0.0.2" {
		t.Fatalf("X-Real-IP: got %q", got)
	}

	r.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.2")
	if got := ClientIP(r); got != "203.0.113.1" {
		t.Fatalf("XFF: got %q", got)
	}
}

func TestSanitizeScreenshot(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","result":{"tab":"a","seq":1,"data":"` + strings.Repeat("A", 100) + `"},"id":1}`)
	out := SanitizeResponse(protocol.MethodScreenshot, in)
	var env struct {
		Result struct {
			Data map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Data["_omitted"] != "base64 image" {
		t.Fatalf("omitted: %#v", env.Result.Data)
	}
}

func TestSanitizeFetchBody(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","result":{"tab":"a","seq":1,"ok":true,"status":200,"result":{"body":"hello"}},"id":1}`)
	out := SanitizeResponse(protocol.MethodFetch, in)
	var env struct {
		Result struct {
			Result struct {
				Body map[string]any `json:"body"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Result.Body["_omitted"] != "response body" {
		t.Fatalf("body: %#v", env.Result.Result.Body)
	}
}

func TestSanitizeObsTruncates(t *testing.T) {
	big := strings.Repeat("x", maxObsEventData+100)
	in, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"tab": "a", "seq": 1, "cursor": 0,
			"events": []map[string]any{{"seq": 1, "data": big}},
		},
		"id": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := SanitizeResponse(protocol.MethodNetwork, in)
	var env struct {
		Result struct {
			Events []struct {
				Data map[string]any `json:"data"`
			} `json:"events"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Events[0].Data["_truncated"] != true {
		t.Fatalf("truncated: %#v", env.Result.Events[0].Data)
	}
}

func TestSanitizePassthrough(t *testing.T) {
	in := []byte(`{"jsonrpc":"2.0","result":{"tab":"a","seq":1},"id":1}`)
	out := SanitizeResponse(protocol.MethodTabList, in)
	if string(out) != string(in) {
		t.Fatalf("unexpected change: %s", out)
	}
}
