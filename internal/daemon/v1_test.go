package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/protocol"
)

type fakeTabs struct {
	infos []*target.Info
	err   error
}

func (f fakeTabs) PageTargets() ([]*target.Info, error) {
	return f.infos, f.err
}

func TestV1TabListReturnsTabsAndSeq(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = fakeTabs{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "t", URL: "https://ex"},
	}}

	body := bytes.NewBufferString(`{"action":"tab_list"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var got protocol.TabListOK
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Seq != 1 {
		t.Fatalf("seq %d want 1", got.Seq)
	}
	if len(got.Tabs) != 1 || got.Tabs[0].Tab != "3456" {
		t.Fatalf("tabs %#v", got.Tabs)
	}
	if got.Tab != "" || got.Focus != "" {
		t.Fatalf("tab/focus without tab_select: tab=%q focus=%q", got.Tab, got.Focus)
	}
}

func TestV1TabListTabMatchesFocusAfterSelect(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = fakeTabs{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	post(`{"action":"tab_select","tab":"3456"}`)
	rec := post(`{"action":"tab_list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var got protocol.TabListOK
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tab != "3456" || got.Focus != "3456" {
		t.Fatalf("tab=%q focus=%q", got.Tab, got.Focus)
	}
}

func TestV1TabSelectUnknownTab(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = fakeTabs{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}

	body := bytes.NewBufferString(`{"action":"tab_select","tab":"9999"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var pe protocol.V1Error
	if err := json.Unmarshal(rec.Body.Bytes(), &pe); err != nil {
		t.Fatal(err)
	}
	if pe.Error == "" {
		t.Fatal("expected error body")
	}
}

func TestV1SeqMonotonicAcrossCalls(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = fakeTabs{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}

	do := func(action, tab string) uint64 {
		t.Helper()
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]string{"action": action, "tab": tab})
		req := httptest.NewRequest(http.MethodPost, "/v1", &buf)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", action, rec.Code, rec.Body.String())
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		var seq uint64
		if err := json.Unmarshal(m["seq"], &seq); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	a := do("tab_list", "")
	b := do("tab_select", "3456")
	c := do("tab_list", "")
	if !(a < b && b < c) {
		t.Fatalf("seq not increasing: %d %d %d", a, b, c)
	}
}
