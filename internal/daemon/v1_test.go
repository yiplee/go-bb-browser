package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/protocol"
)

type fakeConn struct {
	infos       []*target.Info
	pageErr     error
	createID    target.ID
	createSeq   int
	createErr   error
	closeErr    error
	navigateErr error
}

func (f *fakeConn) PageTargets() ([]*target.Info, error) {
	if f.pageErr != nil {
		return nil, f.pageErr
	}
	return f.infos, nil
}

func (f *fakeConn) CreatePageTarget(initialURL string) (target.ID, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.createSeq++
	id := f.createID
	if id == "" {
		id = target.ID(fmt.Sprintf("ABCDEF%04d1234", f.createSeq))
	}
	f.infos = append(f.infos, &target.Info{
		TargetID: id,
		Type:     "page",
		Title:    "",
		URL:      initialURL,
	})
	return id, nil
}

func (f *fakeConn) CloseTarget(id target.ID) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	out := f.infos[:0]
	for _, info := range f.infos {
		if info != nil && info.TargetID != id {
			out = append(out, info)
		}
	}
	f.infos = out
	return nil
}

func (f *fakeConn) Navigate(tabID target.ID, url string) error {
	return f.navigateErr
}

func TestV1TabListRequiresTabAndReturnsContextTab(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "t", URL: "https://ex"},
	}}

	body := bytes.NewBufferString(`{"action":"tab_list","tab":"3456"}`)
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
	if got.Tab != "3456" {
		t.Fatalf("context tab %q want 3456", got.Tab)
	}
	if len(got.Tabs) != 1 || got.Tabs[0].Tab != "3456" {
		t.Fatalf("tabs %#v", got.Tabs)
	}
}

func TestV1TabListMissingTab(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page"},
	}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(`{"action":"tab_list"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestV1TabSelectUnknownTab(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
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

func TestV1WorkflowTabNewOpenClose(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{
		infos:    []*target.Info{},
		createID: "CAFEBABE0001",
	}

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(body)))
		return rec
	}

	rNew := post(`{"action":"tab_new","url":"about:blank"}`)
	if rNew.Code != http.StatusOK {
		t.Fatalf("tab_new %d %s", rNew.Code, rNew.Body.String())
	}
	var tn protocol.TabNewOK
	if err := json.Unmarshal(rNew.Body.Bytes(), &tn); err != nil {
		t.Fatal(err)
	}
	if tn.Tab == "" {
		t.Fatal("empty tab from tab_new")
	}

	rOpen := post(`{"action":"open","tab":"` + tn.Tab + `","url":"https://example.com"}`)
	if rOpen.Code != http.StatusOK {
		t.Fatalf("open %d %s", rOpen.Code, rOpen.Body.String())
	}

	rClose := post(`{"action":"tab_close","tab":"` + tn.Tab + `"}`)
	if rClose.Code != http.StatusOK {
		t.Fatalf("tab_close %d %s", rClose.Code, rClose.Body.String())
	}
}

func TestV1SeqMonotonicAcrossCalls(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{
		infos: []*target.Info{
			{TargetID: "ABCDEF123456", Type: "page"},
		},
	}

	do := func(body string) uint64 {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1", bytes.NewBufferString(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
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

	a := do(`{"action":"tab_list","tab":"3456"}`)
	b := do(`{"action":"tab_select","tab":"3456"}`)
	c := do(`{"action":"tab_list","tab":"3456"}`)
	if !(a < b && b < c) {
		t.Fatalf("seq not increasing: %d %d %d", a, b, c)
	}
}
