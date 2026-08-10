package main

import (
	"log/slog"
	"testing"
)

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
