package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
)

const browserHealthProbeMinInterval = 2 * time.Second

var errBrowserNotConnected = errors.New("browser not connected")

// isCDPSessionLost reports errors that mean the browser CDP websocket/session is gone.
// context.Canceled is excluded — that is usually request shutdown, not CDP loss.
func isCDPSessionLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
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
	s.watchCDPSession(sess)
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

// watchCDPSession exits the daemon when chromedp tears down the browser websocket
// (Chrome quit, debug port gone, etc.) so systemd/docker compose can restart us.
func (s *Server) watchCDPSession(sess *browser.Session) {
	if sess == nil || s.SkipBrowserAttach || s.tabHook != nil {
		return
	}
	go func() {
		<-sess.Context().Done()
		err := sess.Context().Err()
		if err == nil {
			err = errors.New("cdp session ended")
		}
		s.fatalCDPLost(err)
	}()
}

// fatalCDPLost stops the daemon with a non-zero exit so a supervisor reconnects cleanly.
func (s *Server) fatalCDPLost(err error) {
	if err == nil {
		err = errors.New("cdp session lost")
	}
	s.fatalOnce.Do(func() {
		wrapped := fmt.Errorf("cdp session lost: %w", err)
		if s.logger != nil {
			s.logger.Error("cdp disconnected, exiting for supervisor restart", "err", err)
		}
		if s.stopRun != nil {
			s.stopRun()
		}
		select {
		case s.fatalCh <- wrapped:
		default:
		}
	})
}

// ensureBrowserSession verifies the CDP session is still alive. Skipped when
// SkipBrowserAttach or tabHook is set. Does not reconnect — a lost session exits.
func (s *Server) ensureBrowserSession(ctx context.Context) error {
	if s.SkipBrowserAttach || s.tabHook != nil {
		return nil
	}

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
	if err == nil {
		s.markBrowserOK()
		return nil
	}
	if isCDPSessionLost(err) {
		s.fatalCDPLost(err)
		return errBrowserNotConnected
	}
	return err
}
