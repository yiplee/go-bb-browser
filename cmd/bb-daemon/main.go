package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/yiplee/go-bb-browser/internal/daemon"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit (shorthand)")

	debuggerURL := flag.String("debugger-url", envOrDefault("BB_BROWSER_DEBUGGER_URL", ""), "Chrome DevTools endpoint (ws/http URL or host:port); required")
	listen := flag.String("listen", envOrDefault("BB_BROWSER_LISTEN", daemon.DefaultListenAddr), "HTTP listen address for the daemon API")
	tabIdleTimeout := flag.String("tab-idle-timeout", envOrDefault("BB_BROWSER_TAB_IDLE_TIMEOUT", "5m"), "close daemon-created tabs after this idle period (0 disables)")
	cdpOpTimeout := flag.String("cdp-op-timeout", envOrDefault("BB_BROWSER_CDP_OP_TIMEOUT", "30s"), "per-operation CDP timeout (0 uses default 30s)")
	stateDir := flag.String("state-dir", envOrDefault("BB_BROWSER_STATE_DIR", ""), "directory for persisted managed-tab state (default: ~/.local/state/bb-daemon)")
	maxLogBytes := flag.Int64("rpc-log-max-bytes", envOrDefaultInt64("BB_BROWSER_RPC_LOG_MAX_BYTES", daemon.DefaultMaxLogBytes), "rotate rpc.jsonl once it exceeds this many bytes")
	flag.Parse()

	if showVersion {
		fmt.Printf("bb-daemon %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	idleTimeout, err := time.ParseDuration(*tabIdleTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid tab-idle-timeout %q: %v\n", *tabIdleTimeout, err)
		return 2
	}
	cdpTimeout, err := time.ParseDuration(*cdpOpTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid cdp-op-timeout %q: %v\n", *cdpOpTimeout, err)
		return 2
	}

	cfg := daemon.Config{
		DebuggerURL:    *debuggerURL,
		ListenAddr:     *listen,
		TabIdleTimeout: idleTimeout,
		CDPOpTimeout:   cdpTimeout,
		StateDir:       *stateDir,
		MaxLogBytes:    *maxLogBytes,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := daemon.NewServer(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}
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

func envOrDefaultInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
