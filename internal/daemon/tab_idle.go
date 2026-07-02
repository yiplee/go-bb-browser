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

func (s *Server) markTabManaged(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.MarkManaged(tid)
		s.persistTabIdle()
	}
}

func (s *Server) touchTabActivity(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.Touch(tid)
	}
}

func (s *Server) persistTabIdle() {
	if s.tabState == nil || s.tabIdle == nil {
		return
	}
	if err := s.tabState.Save(s.tabIdle.Snapshot()); err != nil && s.logger != nil {
		s.logger.Warn("save tab state failed", "err", err)
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
	present := make(map[target.ID]struct{}, len(snaps))
	for _, sn := range snaps {
		if sn.TargetID == "" {
			continue
		}
		present[sn.TargetID] = struct{}{}
	}
	s.tabIdle.SyncPresent(present)
}

func (s *Server) reconcileIdleFromDisk(snaps []state.TabSnapshot) {
	if s.tabIdle == nil || s.tabState == nil {
		return
	}
	loaded, err := s.tabState.Load()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("load tab state failed", "err", err)
		}
		loaded = map[target.ID]time.Time{}
	}

	now := time.Now()
	timeout := s.cfg.TabIdleTimeout
	grace := s.cfg.IdleStartupGrace
	minLast := now
	if timeout > grace {
		minLast = now.Add(-(timeout - grace))
	}

	present := make(map[target.ID]struct{}, len(snaps))
	for _, sn := range snaps {
		if sn.TargetID == "" {
			continue
		}
		present[sn.TargetID] = struct{}{}
		if last, ok := loaded[sn.TargetID]; ok {
			effective := last
			if effective.Before(minLast) {
				effective = minLast
			}
			s.tabIdle.MarkManagedAt(sn.TargetID, effective)
		}
	}
	s.tabIdle.SyncPresent(present)
	s.persistTabIdle()
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
		s.tabIdle.Forget(tid)
		s.persistTabIdle()
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
	for _, tid := range s.tabIdle.Expired(time.Now(), timeout) {
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
		short, ok := s.tabs.ShortForTarget(tid)
		if !ok {
			if s.tabIdle != nil {
				s.tabIdle.Forget(tid)
			}
			continue
		}
		if err := s.closeTabByShort(short); err != nil {
			if errors.Is(err, errTabCloseUnknownID) {
				if s.tabIdle != nil {
					s.tabIdle.Forget(tid)
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
	s.persistTabIdle()
}
