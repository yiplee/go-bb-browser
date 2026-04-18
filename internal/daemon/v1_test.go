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
	"github.com/yiplee/go-bb-browser/internal/state"
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

func (f *fakeConn) Reload(target.ID) error {
	return nil
}

func (f *fakeConn) Screenshot(target.ID, string) ([]byte, string, error) {
	return []byte{1, 2, 3}, "image/png", nil
}

func (f *fakeConn) Eval(target.ID, string) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}

func (f *fakeConn) Click(target.ID, string) error {
	return nil
}

func (f *fakeConn) Fill(target.ID, string, string) error {
	return nil
}

func (f *fakeConn) DetectForegroundShort([]state.TabSnapshot) (string, bool) {
	return "", false
}

func rpcReq(method string, params any, id any) string {
	m := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		m["params"] = params
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestV1TabListWithoutTabParam(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "t", URL: "https://ex"},
	}}

	body := bytes.NewBufferString(rpcReq(protocol.MethodTabList, map[string]any{}, 1))
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result protocol.TabListResult `json:"result"`
		ID     int                    `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.ID != 1 {
		t.Fatalf("id %d want 1", env.ID)
	}
	if env.Result.Seq != 1 {
		t.Fatalf("seq %d want 1", env.Result.Seq)
	}
	if env.Result.Tab != "3456" {
		t.Fatalf("tab %q want 3456 (first when no focus)", env.Result.Tab)
	}
	if len(env.Result.Tabs) != 1 || env.Result.Tabs[0].Tab != "3456" {
		t.Fatalf("tabs %#v", env.Result.Tabs)
	}
}

func TestV1TabFocus(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "t", URL: "https://ex"},
	}}

	body := bytes.NewBufferString(rpcReq(protocol.MethodTabFocus, map[string]any{}, 1))
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result protocol.TabFocusResult `json:"result"`
		ID     int                     `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.ID != 1 {
		t.Fatalf("id %d want 1", env.ID)
	}
	if env.Result.Seq != 1 {
		t.Fatalf("seq %d want 1", env.Result.Seq)
	}
	if env.Result.Tab != "3456" {
		t.Fatalf("tab %q want 3456", env.Result.Tab)
	}
	if env.Result.Title != "t" || env.Result.URL != "https://ex" {
		t.Fatalf("meta title=%q url=%q", env.Result.Title, env.Result.URL)
	}
}

func TestV1TabFocusNoTabs(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{}}

	body := bytes.NewBufferString(rpcReq(protocol.MethodTabFocus, map[string]any{}, 1))
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var env struct {
		Error *protocol.ResponseError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("got %#v", env.Error)
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

	body := bytes.NewBufferString(rpcReq(protocol.MethodTabSelect, map[string]string{"tab": "9999"}, 1))
	req := httptest.NewRequest(http.MethodPost, "/v1", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var env struct {
		Error *protocol.ResponseError `json:"error"`
		ID    int                     `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Message == "" {
		t.Fatal("expected error object")
	}
	if env.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("code %d", env.Error.Code)
	}
}

func TestV1WorkflowTabNewGotoClose(t *testing.T) {
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

	rNew := post(rpcReq(protocol.MethodTabNew, map[string]string{"url": "about:blank"}, 1))
	if rNew.Code != http.StatusOK {
		t.Fatalf("tab_new %d %s", rNew.Code, rNew.Body.String())
	}
	var newEnv struct {
		Result protocol.TabNewResult `json:"result"`
	}
	if err := json.Unmarshal(rNew.Body.Bytes(), &newEnv); err != nil {
		t.Fatal(err)
	}
	if newEnv.Result.Tab == "" {
		t.Fatal("empty tab from tab_new")
	}

	rGoto := post(rpcReq(protocol.MethodGoto, map[string]string{
		"tab": newEnv.Result.Tab,
		"url": "https://example.com",
	}, 2))
	if rGoto.Code != http.StatusOK {
		t.Fatalf("goto %d %s", rGoto.Code, rGoto.Body.String())
	}

	rReload := post(rpcReq(protocol.MethodReload, map[string]string{"tab": newEnv.Result.Tab}, 3))
	if rReload.Code != http.StatusOK {
		t.Fatalf("reload %d %s", rReload.Code, rReload.Body.String())
	}

	rClose := post(rpcReq(protocol.MethodTabClose, map[string]string{"tab": newEnv.Result.Tab}, 4))
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
		var inner struct {
			Seq uint64 `json:"seq"`
		}
		if err := json.Unmarshal(m["result"], &inner); err != nil {
			t.Fatal(err)
		}
		return inner.Seq
	}

	a := do(rpcReq(protocol.MethodTabList, map[string]any{}, 1))
	b := do(rpcReq(protocol.MethodTabSelect, map[string]string{"tab": "3456"}, 2))
	c := do(rpcReq(protocol.MethodTabList, map[string]any{}, 3))
	if !(a < b && b < c) {
		t.Fatalf("seq not increasing: %d %d %d", a, b, c)
	}
}

func TestV1UnknownMethod(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	srv.tabHook = &fakeConn{infos: []*target.Info{}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1",
		bytes.NewBufferString(`{"jsonrpc":"2.0","method":"nope","id":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var env struct {
		Error *protocol.ResponseError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != protocol.CodeMethodNotFound {
		t.Fatalf("got %#v", env.Error)
	}
}

func TestV1MissingID(t *testing.T) {
	cfg := Config{DebuggerURL: "127.0.0.1:9222", ListenAddr: "127.0.0.1:0"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1",
		bytes.NewBufferString(`{"jsonrpc":"2.0","method":"tab_list"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}
