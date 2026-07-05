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
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
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

	// lastBrowserOK is updated after a successful CDP health probe (see ensureBrowserSession).
	lastBrowserOK time.Time

	obsStore *state.TabObsStore
	obsSink  *obsSink
	tabIdle  *state.TabIdleTracker

	tabMuOps    sync.Mutex
	tabCDPLocks map[string]*sync.Mutex // per short tab id

	// auditWG tracks in-flight async audit writes so they are drained before the store closes.
	auditWG sync.WaitGroup

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
		cfg:      cfg,
		logger:   logger,
		mux:      http.NewServeMux(),
		tabs:     state.NewTabRegistry(),
		obsStore: obsStore,
		tabIdle:  state.NewTabIdleTracker(),
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
	s.routes()
	return s, nil
}

// syncObservation aligns CDP tab observers and clears buffers for removed targets (Phase 3).
func (s *Server) syncObservation(conn tabConn, infos []*target.Info) {
	if s.obsStore == nil {
		return
	}
	if sess, ok := conn.(*browser.Session); ok && s.obsSink != nil {
		sess.SyncObservers(sess.Context(), infos, s.obsSink, s.logger)
	}
	s.obsStore.SyncPresence(infos)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealthRoute)
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
	result := s.healthResult(r.Context())
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
	defer func() {
		if s.store != nil {
			s.auditWG.Wait() // drain async audit writes before closing the store
			_ = s.store.Close()
		}
	}()

	if !s.SkipBrowserAttach && s.tabHook == nil {
		s.tabMu.Lock()
		if err := s.connectBrowserLocked(ctx); err != nil {
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
		go s.runTabIdleSweeper(ctx)
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
	case err := <-errCh:
		return err
	}
}
