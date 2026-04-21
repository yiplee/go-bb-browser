package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yiplee/go-bb-browser/internal/protocol"
)

func TestHealth_ok(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHealth_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Health(context.Background())
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want HTTPError 503, got %v", err)
	}
}

func TestCall_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"seq":42,"tabs":[],"focus":"abc","tab":"abc"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	var got protocol.TabListResult
	if err := c.Call(context.Background(), protocol.MethodTabList, protocol.TabListParams{}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Seq != 42 || got.Focus != "abc" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestCall_rpcError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found","data":{"error":"unknown method","method":"nope"}}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Call(context.Background(), "nope", map[string]any{}, new(protocol.TabListResult))
	var re *RPCError
	if !errors.As(err, &re) {
		t.Fatalf("want *RPCError, got %T %v", err, err)
	}
	if re.Code != protocol.CodeMethodNotFound {
		t.Fatalf("code: got %d", re.Code)
	}
	var data protocol.ErrData
	if err := re.UnmarshalData(&data); err != nil {
		t.Fatal(err)
	}
	if data.Method != "nope" {
		t.Fatalf("data: %+v", data)
	}
}

func TestCall_httpErrorOnV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Call(context.Background(), protocol.MethodTabList, protocol.TabListParams{}, new(protocol.TabListResult))
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("want HTTPError 405, got %v", err)
	}
}

func TestTabList_wrapper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(b, &req)
		if req.Method != protocol.MethodTabList {
			t.Errorf("method %q", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":{"seq":1,"tabs":[{"tab":"1","title":"t","url":"u"}]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	out, err := c.TabList(context.Background(), protocol.TabListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tabs) != 1 || out.Tabs[0].Tab != "1" {
		t.Fatalf("tabs: %+v", out.Tabs)
	}
}

func TestCall_missingResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Call(context.Background(), protocol.MethodTabList, protocol.TabListParams{}, new(protocol.TabListResult))
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
}
