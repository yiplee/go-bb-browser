package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yiplee/go-bb-browser/internal/daemon"
)

func main() {
	os.Exit(run())
}

func run() int {
	debuggerURL := flag.String("debugger-url", envOrDefault("BB_BROWSER_DEBUGGER_URL", ""), "Chrome DevTools endpoint (ws/http URL or host:port); required")
	listen := flag.String("listen", envOrDefault("BB_BROWSER_LISTEN", daemon.DefaultListenAddr), "HTTP listen address for the daemon API")
	flag.Parse()

	cfg := daemon.Config{
		DebuggerURL: *debuggerURL,
		ListenAddr:  *listen,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := daemon.NewServer(cfg, log)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("daemon exited", "err", err)
		return 1
	}
	return 0
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
