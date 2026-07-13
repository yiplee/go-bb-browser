package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/internal/store"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

// Server is the HTTP API front-end for the daemon (JSON-RPC over POST /v1).
type Server struct {
	cfg    Config
	logger *slog.Logger
	mux    *http.ServeMux

	store    *store.Store
	tabs     *state.TabRegistry
	tabMu    sync.RWMutex
	tabLive  tabConn // set after CDP connect in ListenAndServe
	tabHook  tabConn // optional: tests inject fake CDP
	sessDone func()  // releases browser session references on shutdown (does not close Chrome)

	browserState     sessionState
	browserFailures  int
	browserLastProbe time.Time
	probeTrigger     chan struct{}
	probeInFlight    atomic.Bool

	obsStore *state.TabObsStore
	obsSink  *obsSink
	tabIdle  *state.TabIdleTracker

	tabMuOps    sync.Mutex
	tabCDPLocks map[string]*tabLockEntry

	auditCh   chan store.LogRecord
	auditDone chan struct{}
	auditWG   sync.WaitGroup

	// CDP loss triggers fatal exit (supervisor restart) instead of in-process reconnect.
	fatalOnce sync.Once
	fatalCh   chan error
	stopRun   context.CancelFunc

	// SkipBrowserAttach skips CDP connect in ListenAndServe (tests without Chrome).
	SkipBrowserAttach bool
}

// NewServer builds a daemon HTTP server with the given config (must pass Validate).
func NewServer(cfg Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	obsStore := state.NewTabObsStore()
	s := &Server{
		cfg:          cfg,
		logger:       logger,
		mux:          http.NewServeMux(),
		tabs:         state.NewTabRegistry(),
		obsStore:     obsStore,
		tabIdle:      state.NewTabIdleTracker(),
		probeTrigger: make(chan struct{}, 1),
		auditCh:      make(chan store.LogRecord, 256),
		auditDone:    make(chan struct{}),
	}
	stateDir := effectiveStateDir(cfg)
	if strings.TrimSpace(cfg.StateDir) == stateDirDisabled {
		stateDir = stateDirDisabled
	}
	st, err := store.Open(store.OpenConfig{StateDir: stateDir, Logger: logger, MaxLogBytes: cfg.MaxLogBytes})
	if err != nil {
		return nil, fmt.Errorf("rpc log store: %w", err)
	}
	s.store = st
	s.obsSink = &obsSink{store: st, obs: obsStore, logger: logger}
	go s.runAuditWriter()
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealthRoute)
	s.mux.HandleFunc("/live", s.handleLiveRoute)
	s.mux.HandleFunc("/ready", s.handleReadyRoute)
	s.mux.HandleFunc("/v1", s.handleV1)
}

func (s *Server) tabConn() tabConn {
	if s.tabHook != nil {
		return s.tabHook
	}
	s.tabMu.RLock()
	defer s.tabMu.RUnlock()
	return s.tabLive
}

func (s *Server) Handler() http.Handler {
	return http.MaxBytesHandler(s.mux, s.cfg.MaxBodyBytes)
}

func (s *Server) handleHealthRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleHealthGet(w, r)
		return
	}
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleHealthGet(w http.ResponseWriter, r *http.Request) {
	result := s.healthResult(false)
	s.writeHealthResult(w, result)
}

func (s *Server) handleReadyRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeHealthResult(w, s.healthResult(true))
}

func (s *Server) handleLiveRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := json.Marshal(protocol.LivenessResult{Status: "ok"})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) writeHealthResult(w http.ResponseWriter, result protocol.HealthResult) {
	b, err := json.Marshal(result)
	if err != nil {
		s.logger.Error("health json encode failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if result.Browser == protocol.HealthBrowserDisconnected {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("health response write failed", "err", err)
	}
}

// ListenAndServe starts the HTTP server until ctx is cancelled or Listen fails.
func (s *Server) ListenAndServe(ctx context.Context) error {
	runCtx, stopRun := context.WithCancel(ctx)
	s.stopRun = stopRun
	s.fatalCh = make(chan error, 1)

	defer func() {
		if s.store != nil {
			close(s.auditCh)
			<-s.auditDone
			_ = s.store.Close()
		}
	}()

	if !s.SkipBrowserAttach && s.tabHook == nil {
		s.tabMu.Lock()
		if err := s.connectBrowserLocked(runCtx); err != nil {
			s.tabMu.Unlock()
			return fmt.Errorf("cdp attach: %w", err)
		}
		s.tabMu.Unlock()
		defer func() {
			s.tabMu.Lock()
			if s.sessDone != nil {
				s.sessDone()
			}
			s.tabLive = nil
			s.sessDone = nil
			s.tabMu.Unlock()
		}()
	}

	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("daemon listening", "addr", ln.Addr().String(), "debugger", s.cfg.DebuggerURL)
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- http.ErrServerClosed
	}()

	if s.cfg.TabIdleTimeout > 0 {
		go s.runTabIdleSweeper(runCtx)
	}
	if s.cfg.ObserverIdleTimeout > 0 {
		go s.runObserverSweeper(runCtx)
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		<-errCh
		return nil
	case err := <-s.fatalCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-errCh
		return err
	case err := <-errCh:
		return err
	}
}

func (s *Server) runAuditWriter() {
	defer close(s.auditDone)
	for rec := range s.auditCh {
		if err := s.store.AppendRPC(rec); err != nil && s.logger != nil {
			s.logger.Warn("append rpc log failed", "err", err, "action", rec.Action)
		}
		s.auditWG.Done()
	}
}
