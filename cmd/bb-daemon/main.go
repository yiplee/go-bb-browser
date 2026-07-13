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
	watchdogInterval := flag.String("cdp-watchdog-interval", envOrDefault("BB_BROWSER_CDP_WATCHDOG_INTERVAL", "5s"), "interval between Browser.getVersion watchdog probes")
	watchdogTimeout := flag.String("cdp-watchdog-timeout", envOrDefault("BB_BROWSER_CDP_WATCHDOG_TIMEOUT", "2s"), "timeout for one CDP watchdog probe")
	watchdogFailures := flag.Int("cdp-watchdog-failures", envOrDefaultInt("BB_BROWSER_CDP_WATCHDOG_FAILURES", 3), "consecutive failed CDP probes before daemon exit")
	observerIdleTimeout := flag.String("observer-idle-timeout", envOrDefault("BB_BROWSER_OBSERVER_IDLE_TIMEOUT", "5m"), "disable idle observation domains after this period (0 keeps them enabled until tab close)")
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
	wdInterval, err := time.ParseDuration(*watchdogInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid cdp-watchdog-interval %q: %v\n", *watchdogInterval, err)
		return 2
	}
	wdTimeout, err := time.ParseDuration(*watchdogTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid cdp-watchdog-timeout %q: %v\n", *watchdogTimeout, err)
		return 2
	}
	obsIdle, err := time.ParseDuration(*observerIdleTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid observer-idle-timeout %q: %v\n", *observerIdleTimeout, err)
		return 2
	}

	cfg := daemon.Config{
		DebuggerURL:         *debuggerURL,
		ListenAddr:          *listen,
		TabIdleTimeout:      idleTimeout,
		StateDir:            *stateDir,
		MaxLogBytes:         *maxLogBytes,
		CDPWatchdogInterval: wdInterval,
		CDPWatchdogTimeout:  wdTimeout,
		CDPWatchdogFailures: *watchdogFailures,
		ObserverIdleTimeout: obsIdle,
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

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
