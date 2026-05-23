package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/state"
)

var (
	errTabCloseNoConn    = errors.New("browser session not ready")
	errTabCloseUnknownID = errors.New("unknown tab id")
)

func (s *Server) markTabManaged(short string) {
	if s.tabIdle != nil {
		s.tabIdle.MarkManaged(short)
	}
}

func (s *Server) touchTabActivity(short string) {
	if s.tabIdle != nil {
		s.tabIdle.Touch(short)
	}
}

func (s *Server) syncTabsFromTargets(infos []*target.Info) []state.TabSnapshot {
	snaps := s.tabs.SyncPageTargets(infos)
	s.syncTabIdlePresence(snaps)
	return snaps
}

func (s *Server) syncTabIdlePresence(snaps []state.TabSnapshot) {
	if s.tabIdle == nil {
		return
	}
	present := make(map[string]struct{}, len(snaps))
	for _, sn := range snaps {
		present[sn.ShortID] = struct{}{}
	}
	s.tabIdle.SyncPresent(present)
}

func (s *Server) closeTabByShort(tab string) error {
	conn := s.tabConn()
	if conn == nil {
		return errTabCloseNoConn
	}
	unlock := s.lockTab(tab)
	defer unlock()

	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.syncTabsFromTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		return errTabCloseUnknownID
	}
	if err := conn.CloseTarget(tid); err != nil {
		return err
	}
	targets, ptErr := conn.PageTargets()
	if ptErr == nil {
		s.syncTabsFromTargets(targets)
		s.syncObservation(conn, targets)
	}
	if s.tabIdle != nil {
		s.tabIdle.Forget(tab)
	}
	return nil
}

func (s *Server) runTabIdleSweeper(ctx context.Context) {
	timeout := s.cfg.TabIdleTimeout
	if timeout <= 0 || s.tabIdle == nil {
		return
	}
	interval := timeout / 6
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.closeExpiredTabs(ctx)
		}
	}
}

func (s *Server) closeExpiredTabs(ctx context.Context) {
	timeout := s.cfg.TabIdleTimeout
	if timeout <= 0 || s.tabIdle == nil {
		return
	}
	for _, short := range s.tabIdle.Expired(time.Now(), timeout) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := s.ensureBrowserSession(ctx); err != nil {
			if s.logger != nil {
				s.logger.Warn("tab idle cleanup skipped", "err", err)
			}
			return
		}
		if err := s.closeTabByShort(short); err != nil {
			if errors.Is(err, errTabCloseUnknownID) {
				if s.tabIdle != nil {
					s.tabIdle.Forget(short)
				}
				continue
			}
			if s.logger != nil {
				s.logger.Warn("tab idle cleanup failed", "tab", short, "err", err)
			}
			continue
		}
		if s.logger != nil {
			s.logger.Info("tab idle closed", "tab", short, "idle", timeout)
		}
	}
}
