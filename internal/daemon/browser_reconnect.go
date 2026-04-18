package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yiplee/go-bb-browser/internal/browser"
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
	sess, err := browser.Connect(context.WithoutCancel(ctx), s.cfg.DebuggerURL)
	if err != nil {
		return err
	}
	s.tabLive = sess
	s.sessDone = sess.Close
	targets, terr := sess.PageTargets()
	if terr != nil {
		if s.logger != nil {
			s.logger.Warn("list targets after browser connect", "err", terr)
		}
		targets = nil
	}
	s.tabs.SyncPageTargets(targets)
	s.syncObservation(sess, targets)
	s.lastBrowserOK = time.Now()
	if s.logger != nil {
		s.logger.Info("browser CDP session connected", "debugger", s.cfg.DebuggerURL)
	}
	return nil
}

// ensureBrowserSession (re)establishes the CDP session when Chrome was restarted or the
// websocket dropped. Skipped when SkipBrowserAttach or tabHook is set.
func (s *Server) ensureBrowserSession(ctx context.Context) error {
	if s.SkipBrowserAttach || s.tabHook != nil {
		return nil
	}

	s.tabMu.Lock()
	defer s.tabMu.Unlock()

	if s.tabLive != nil {
		if bs, ok := s.tabLive.(*browser.Session); ok {
			if time.Since(s.lastBrowserOK) < browserHealthProbeMinInterval {
				return nil
			}
			_, err := bs.PageTargets()
			if err == nil {
				s.lastBrowserOK = time.Now()
				return nil
			}
			if !isReconnectableCDPErr(err) {
				return err
			}
			if s.logger != nil {
				s.logger.Warn("cdp session unhealthy, reconnecting", "err", err)
			}
			if s.sessDone != nil {
				s.sessDone()
			}
			s.tabLive = nil
			s.sessDone = nil
		}
	}

	return s.connectBrowserLocked(ctx)
}
