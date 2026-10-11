package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIAPIToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		flags []string
		want  string
	}{
		{"disabled", "", nil, ""},
		{"environment", "env-secret", nil, "Bearer env-secret"},
		{"flag overrides environment", "env-secret", []string{"--api-token", "flag-secret"}, "Bearer flag-secret"},
		{"empty flag disables environment", "env-secret", []string{"--api-token", ""}, ""},
		{"trim whitespace", " env-secret ", nil, "Bearer env-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BB_BROWSER_API_TOKEN", tc.env)
			var got string
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/v1" {
					t.Error("unexpected request")
				}
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"seq":1,"tabs":[]}}`))
			}))
			defer srv.Close()
			root := newRootCmd()
			root.SetArgs(append([]string{"--url", srv.URL, "tab", "list"}, tc.flags...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || got != tc.want {
				t.Fatalf("requests=%d Authorization=%q, want %q", requests, got, tc.want)
			}
		})
	}
}

func TestCLIHelpDoesNotExposeAPIToken(t *testing.T) {
	t.Setenv("BB_BROWSER_API_TOKEN", "help-secret")
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "help-secret") || !strings.Contains(out.String(), "--api-token") {
		t.Fatal("help omitted the flag or exposed the token")
	}
}

func TestFormatObsLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{
			name: "log entry with url prints text",
			data: `{"source":"javascript","level":"error","text":"failed to fetch","url":"https://example.com/app.js","lineNumber":12}`,
			want: "failed to fetch",
		},
		{
			name: "exception text",
			data: `{"text":"Uncaught TypeError","lineNumber":3}`,
			want: "Uncaught TypeError",
		},
		{
			name: "empty text falls back to raw json",
			data: `{"text":"","url":"https://example.com/app.js"}`,
			want: `{"text":"","url":"https://example.com/app.js"}`,
		},
		{
			name: "console type",
			data: `{"type":"warning","args":[]}`,
			want: `warning {"type":"warning","args":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatObsLine(json.RawMessage(tc.data)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
