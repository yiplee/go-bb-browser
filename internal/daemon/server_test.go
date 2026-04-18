package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	cfg := Config{
		DebuggerURL: "127.0.0.1:9222",
		ListenAddr:  "127.0.0.1:0",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body: %#v", body)
	}
}
