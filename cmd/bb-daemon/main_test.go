package main

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yiplee/go-bb-browser/internal/daemon"
)

func TestAPITokenFlagsAndSources(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(file, []byte("# comment\nfile-secret\n env-secret \n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_BROWSER_API_TOKEN", "env-secret,flag-secret, ,env-secret")
	t.Setenv("BB_BROWSER_API_TOKEN_FILE", file)
	fs := flag.NewFlagSet("bb-daemon", flag.ContinueOnError)
	var flags apiTokenFlags
	fs.Var(&flags, "api-token", "allowed API token")
	path := fs.String("api-token-file", envOrDefault("BB_BROWSER_API_TOKEN_FILE", ""), "token file")
	if err := fs.Parse([]string{"--api-token", " flag-secret ", "--api-token", "second-secret", "--api-token", ""}); err != nil {
		t.Fatal(err)
	}
	tokens, err := daemon.LoadAPITokens(flags, os.Getenv("BB_BROWSER_API_TOKEN"), *path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"flag-secret", "second-secret", "env-secret", "file-secret"}; !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	var usage bytes.Buffer
	fs.SetOutput(&usage)
	fs.PrintDefaults()
	for _, token := range tokens {
		if strings.Contains(usage.String(), token) {
			t.Fatal("flag usage exposed a token")
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"", slog.LevelInfo, true},
		{"info", slog.LevelInfo, true},
		{"INFO", slog.LevelInfo, true},
		{" debug ", slog.LevelDebug, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"trace", 0, false},
		{"verbose", 0, false},
	}
	for _, tc := range cases {
		got, err := parseLogLevel(tc.in)
		if tc.ok {
			if err != nil {
				t.Fatalf("parseLogLevel(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseLogLevel(%q)=%v, want %v", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("parseLogLevel(%q): want error", tc.in)
		}
	}
}

func TestNewLogger(t *testing.T) {
	t.Parallel()

	if _, err := newLogger("info", "text"); err != nil {
		t.Fatalf("text: %v", err)
	}
	if _, err := newLogger("debug", "json"); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, err := newLogger("info", "yaml"); err == nil {
		t.Fatal("expected invalid format error")
	}
	if _, err := newLogger("nope", "text"); err == nil {
		t.Fatal("expected invalid level error")
	}
}
