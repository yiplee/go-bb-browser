package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateMiddle(t *testing.T) {
	got := truncateMiddle("short", logStringMaxLen, logStringHead, logStringTail)
	if got != "short" {
		t.Fatalf("short string unchanged: %q", got)
	}

	long := strings.Repeat("a", 100) + strings.Repeat("b", 100) + strings.Repeat("c", 100)
	got = truncateMiddle(long, logStringMaxLen, logStringHead, logStringTail)
	if !strings.Contains(got, logStringEllipsis) {
		t.Fatalf("expected ellipsis in %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 80)) {
		t.Fatalf("expected head prefix, got %q", got[:90])
	}
	if !strings.HasSuffix(got, strings.Repeat("c", 80)) {
		t.Fatalf("expected tail suffix, got %q", got[len(got)-90:])
	}
	if len([]rune(got)) != logStringHead+len(logStringEllipsis)+logStringTail {
		t.Fatalf("unexpected compacted length: %d", len([]rune(got)))
	}
}

func TestCompactRPCLogBody_shortScriptUnchanged(t *testing.T) {
	body := json.RawMessage(`{"jsonrpc":"2.0","method":"eval","params":{"tab":"a1b2","script":"1+1"},"id":1}`)
	got := compactRPCLogBody(body)
	if string(got) != string(body) {
		t.Fatalf("short script unchanged: got %s", got)
	}
}

func TestCompactRPCLogBody_longScript(t *testing.T) {
	script := strings.Repeat("x", 150) + "MIDDLE" + strings.Repeat("y", 150)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "eval",
		"params":  map[string]any{"tab": "a1b2", "script": script},
		"id":      1,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := compactRPCLogBody(body)
	var env struct {
		Params struct {
			Script string `json:"script"`
			Tab    string `json:"tab"`
		} `json:"params"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatal(err)
	}
	if env.Params.Tab != "a1b2" {
		t.Fatalf("tab preserved: %q", env.Params.Tab)
	}
	if !strings.Contains(env.Params.Script, logStringEllipsis) {
		t.Fatalf("expected ellipsis in script: %q", env.Params.Script)
	}
	if len(env.Params.Script) >= len(script) {
		t.Fatalf("script should be shorter: got %d want < %d", len(env.Params.Script), len(script))
	}
}

func TestCompactRPCLogBody_tabStillReadable(t *testing.T) {
	script := strings.Repeat("z", 500)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "eval",
		"params":  map[string]any{"tab": "1234", "script": script},
		"id":      1,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := compactRPCLogBody(body)
	if tab := TabFromRequestBody(got); tab != "1234" {
		t.Fatalf("tab from compacted body: got %q want 1234", tab)
	}
}

func TestCompactRPCLogBody_invalidBodyPassthrough(t *testing.T) {
	cases := []json.RawMessage{
		json.RawMessage(`not json`),
		json.RawMessage(`{"jsonrpc":"2.0","method":"tab_list","id":1}`),
		json.RawMessage(`{"jsonrpc":"2.0","method":"eval","params":["tab","script"],"id":1}`),
	}
	for _, body := range cases {
		got := compactRPCLogBody(body)
		if string(got) != string(body) {
			t.Fatalf("body unchanged for %s: got %s", body, got)
		}
	}
}

func TestAppendRPC_compactsLongScriptOnDisk(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(OpenConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	script := strings.Repeat("s", 300)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "eval",
		"params":  map[string]any{"tab": "aa11", "script": script},
		"id":      1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AppendRPC(LogRecord{
		Action: "eval",
		Body:   body,
		Tab:    "aa11",
		OK:     true,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, rpcLogFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), script) {
		t.Fatal("full script should not appear in rpc.jsonl")
	}
	if !strings.Contains(string(data), logStringEllipsis) {
		t.Fatalf("expected ellipsis in log line: %s", data)
	}
}
