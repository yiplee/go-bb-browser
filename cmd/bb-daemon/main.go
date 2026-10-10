package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	var apiTokens apiTokenFlags
	flag.Var(&apiTokens, "api-token", "allowed API token (repeatable; merged with BB_BROWSER_API_TOKEN and token file)")
	apiTokenFile := flag.String("api-token-file", envOrDefault("BB_BROWSER_API_TOKEN_FILE", ""), "API token file (one token per line; blank and # comment lines ignored)")
	tabIdleTimeout := flag.String("tab-idle-timeout", envOrDefault("BB_BROWSER_TAB_IDLE_TIMEOUT", "5m"), "close daemon-created tabs after this idle period (0 disables)")
	watchdogInterval := flag.String("cdp-watchdog-interval", envOrDefault("BB_BROWSER_CDP_WATCHDOG_INTERVAL", "5s"), "interval between Browser.getVersion watchdog probes")
	watchdogTimeout := flag.String("cdp-watchdog-timeout", envOrDefault("BB_BROWSER_CDP_WATCHDOG_TIMEOUT", "2s"), "timeout for one CDP watchdog probe")
	watchdogFailures := flag.Int("cdp-watchdog-failures", envOrDefaultInt("BB_BROWSER_CDP_WATCHDOG_FAILURES", 3), "consecutive failed CDP probes before daemon exit")
	observerIdleTimeout := flag.String("observer-idle-timeout", envOrDefault("BB_BROWSER_OBSERVER_IDLE_TIMEOUT", "5m"), "disable idle observation domains after this period (0 keeps them enabled until tab close)")
	stateDir := flag.String("state-dir", envOrDefault("BB_BROWSER_STATE_DIR", ""), "directory for persisted managed-tab state (default: ~/.local/state/bb-daemon)")
	maxLogBytes := flag.Int64("rpc-log-max-bytes", envOrDefaultInt64("BB_BROWSER_RPC_LOG_MAX_BYTES", daemon.DefaultMaxLogBytes), "rotate rpc.jsonl once it exceeds this many bytes")
	logLevel := flag.String("log-level", envOrDefault("BB_BROWSER_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
	logFormat := flag.String("log-format", envOrDefault("BB_BROWSER_LOG_FORMAT", "text"), "log format: text or json")
	flag.Parse()

	if showVersion {
		fmt.Printf("bb-daemon %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	idleTimeout, err := time.ParseDuration(*tabIdleTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --tab-idle-timeout: %v\n", err)
		return 2
	}
	wdInterval, err := time.ParseDuration(*watchdogInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --cdp-watchdog-interval: %v\n", err)
		return 2
	}
	wdTimeout, err := time.ParseDuration(*watchdogTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --cdp-watchdog-timeout: %v\n", err)
		return 2
	}
	obsIdle, err := time.ParseDuration(*observerIdleTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --observer-idle-timeout: %v\n", err)
		return 2
	}

	tokens, err := daemon.LoadAPITokens(apiTokens, os.Getenv("BB_BROWSER_API_TOKEN"), *apiTokenFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}
	cfg := daemon.Config{
		APITokens:           tokens,
		DebuggerURL:         *debuggerURL,
		ListenAddr:          *listen,
		TabIdleTimeout:      idleTimeout,
		CDPWatchdogInterval: wdInterval,
		CDPWatchdogTimeout:  wdTimeout,
		CDPWatchdogFailures: *watchdogFailures,
		ObserverIdleTimeout: obsIdle,
		StateDir:            *stateDir,
		MaxLogBytes:         *maxLogBytes,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}

	log, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := daemon.NewServer(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		return 1
	}
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("daemon exited", "err", err)
		return 1
	}
	return 0
}

func newLogger(level, format string) (*slog.Logger, error) {
	lvl, err := parseLogLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log-format %q (want text or json)", format)
	}
}

// String never exposes token values in flag usage or diagnostics.
type apiTokenFlags []string

func (*apiTokenFlags) String() string { return "" }

func (tokens *apiTokenFlags) Set(value string) error {
	*tokens = append(*tokens, value)
	return nil
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid --log-level %q (want debug, info, warn, or error)", level)
	}
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
