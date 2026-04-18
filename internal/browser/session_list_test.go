package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/target"
)

func TestFirstExistingPageTabID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "x1", "type": "service_worker"},
			{"id": "ABC123", "type": "page", "url": "https://a/"},
		})
	}))
	defer ts.Close()

	id, err := firstExistingPageTabID(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if want := target.ID("ABC123"); id != want {
		t.Fatalf("id = %q, want %q", id, want)
	}
}

func TestFirstExistingPageTabIDEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "w", "type": "service_worker"},
		})
	}))
	defer ts.Close()

	id, err := firstExistingPageTabID(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("expected empty id, got %q", id)
	}
}

func TestHTTPDebuggerBase(t *testing.T) {
	if got := httpDebuggerBase("ws://127.0.0.1:9222"); got != "http://127.0.0.1:9222" {
		t.Fatalf("ws: got %q", got)
	}
	if got := httpDebuggerBase("wss://example.com:9222"); got != "https://example.com:9222" {
		t.Fatalf("wss: got %q", got)
	}
}
