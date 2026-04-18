package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server is the HTTP API front-end for the daemon (Phase 0: health only).
type Server struct {
	cfg    Config
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewServer builds a daemon HTTP server with the given config (must pass Validate).
func NewServer(cfg Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, logger: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealthRoute)
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

func (s *Server) handleHealthGet(w http.ResponseWriter, _ *http.Request) {
	b, err := json.Marshal(map[string]string{"status": "ok"})
	if err != nil {
		s.logger.Error("health json encode failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := w.Write(b); err != nil {
		s.logger.Error("health response write failed", "err", err)
	}
}

// ListenAndServe starts the HTTP server until ctx is cancelled or Listen fails.
func (s *Server) ListenAndServe(ctx context.Context) error {
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
