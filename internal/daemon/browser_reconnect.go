package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
)

const browserHealthProbeMinInterval = 2 * time.Second

func isReconnectableCDPErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "eof"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "use of closed network connection"),
		strings.Contains(msg, "websocket"),
		strings.Contains(msg, "channel closed"),
		strings.Contains(msg, "invalid session"),
		strings.Contains(msg, "target closed"),
		strings.Contains(msg, "page has been closed"),
		strings.Contains(msg, "could not dial"):
		return true
	default:
		return false
	}
}

// connectBrowserLocked attaches a new chromedp session. Caller must hold s.tabMu.
func (s *Server) connectBrowserLocked(ctx context.Context) error {
	sess, err := browser.Connect(context.WithoutCancel(ctx), s.cfg.DebuggerURL, browser.ConnectOptions{
		OpTimeout: s.cfg.CDPOpTimeout,
		Logger:    s.logger,
	})
	if err != nil {
		return err
	}
	s.tabLive = sess
	s.sessDone = sess.Close
	targets, terr := sess.PageTargets()
	var snaps []state.TabSnapshot
	if terr != nil {
		if s.logger != nil {
			s.logger.Warn("list targets after browser connect", "err", terr)
		}
	} else {
		snaps = s.syncTabsFromTargets(targets)
		s.syncObservation(sess, targets)
	}
	// Restore managed-tab idle state from the RPC log exactly once, at startup, so
	// long-idle daemon-created tabs are still cleaned up after a restart.
	s.reconcileIdleFromLog(snaps)
	s.lastBrowserOK = time.Now()
	if s.logger != nil {
		s.logger.Info("browser CDP session connected", "debugger", s.cfg.DebuggerURL)
	}
	return nil
}

func (s *Server) markBrowserSessionStale() {
	s.tabMu.Lock()
	s.lastBrowserOK = time.Time{}
	s.tabMu.Unlock()
}

// probeBrowserSession checks CDP health without holding the session write lock.
// Returns nil when the session is healthy or probe is throttled; a non-nil error
// means the caller should reconnect (after verifying the error is reconnectable).
func (s *Server) probeBrowserSession() error {
	s.tabMu.RLock()
	live := s.tabLive
	lastOK := s.lastBrowserOK
	s.tabMu.RUnlock()

	if live == nil {
		return errBrowserNotConnected
	}
	bs, ok := live.(*browser.Session)
	if !ok {
		return nil
	}
	if time.Since(lastOK) < browserHealthProbeMinInterval {
		return nil
	}
	_, err := bs.PageTargets()
	if err != nil {
		return err
	}
	s.tabMu.Lock()
	if s.tabLive == live {
		s.lastBrowserOK = time.Now()
	}
	s.tabMu.Unlock()
	return nil
}

var errBrowserNotConnected = errors.New("browser not connected")

func (s *Server) dropBrowserSessionLocked() {
	if s.sessDone != nil {
		s.sessDone()
	}
	s.tabLive = nil
	s.sessDone = nil
}

// ensureBrowserSession (re)establishes the CDP session when Chrome was restarted or the
// websocket dropped. Skipped when SkipBrowserAttach or tabHook is set.
func (s *Server) ensureBrowserSession(ctx context.Context) error {
	if s.SkipBrowserAttach || s.tabHook != nil {
		return nil
	}

	if err := s.probeBrowserSession(); err != nil {
		if errors.Is(err, errBrowserNotConnected) {
			// fall through to connect
		} else if !isReconnectableCDPErr(err) {
			return err
		} else {
			if s.logger != nil {
				s.logger.Warn("cdp session unhealthy, reconnecting", "err", err)
			}
		}
	} else {
		s.tabMu.RLock()
		connected := s.tabLive != nil
		s.tabMu.RUnlock()
		if connected {
			return nil
		}
	}

	s.tabMu.Lock()
	defer s.tabMu.Unlock()

	if s.tabLive != nil {
		if bs, ok := s.tabLive.(*browser.Session); ok && time.Since(s.lastBrowserOK) >= browserHealthProbeMinInterval {
			if _, err := bs.PageTargets(); err == nil {
				s.lastBrowserOK = time.Now()
				return nil
			} else if !isReconnectableCDPErr(err) {
				return err
			}
		} else if time.Since(s.lastBrowserOK) < browserHealthProbeMinInterval {
			return nil
		}
		s.dropBrowserSessionLocked()
	}

	return s.connectBrowserLocked(ctx)
}
