package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/internal/store"
)

var (
	errTabCloseNoConn    = errors.New("browser session not ready")
	errTabCloseUnknownID = errors.New("unknown tab id")
)

func (s *Server) markTabManaged(tid target.ID, short, openURL string, silent bool) {
	if s.tabIdle != nil {
		s.tabIdle.MarkManaged(tid)
	}
	if s.store != nil {
		now := time.Now().UTC()
		if err := s.store.PutTab(store.TabRecord{
			TargetID:       string(tid),
			ShortID:        short,
			OpenURL:        openURL,
			OpenedAt:       now,
			LastActivityAt: now,
			Silent:         silent,
		}); err != nil && s.logger != nil {
			s.logger.Warn("put tab record failed", "err", err)
		}
	}
}

func (s *Server) touchTabActivity(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.Touch(tid)
	}
	if s.store != nil {
		if err := s.store.TouchTab(string(tid), time.Now().UTC()); err != nil && s.logger != nil {
			s.logger.Warn("touch tab record failed", "err", err)
		}
	}
}

func (s *Server) forgetTabManaged(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.Forget(tid)
	}
	if s.store != nil {
		if err := s.store.DeleteTab(string(tid)); err != nil && s.logger != nil {
			s.logger.Warn("delete tab record failed", "err", err)
		}
	}
}

func (s *Server) syncTabsFromTargets(infos []*target.Info) []state.TabSnapshot {
	snaps := s.tabs.SyncPageTargets(infos)
	s.syncTabIdlePresence(snaps)
	s.pruneStoredTabsMissingFromBrowser(snaps)
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

// pruneStoredTabsMissingFromBrowser removes Badger tab records only after a successful
// PageTargets sync shows the target is no longer present in the browser.
func (s *Server) pruneStoredTabsMissingFromBrowser(snaps []state.TabSnapshot) {
	if s.store == nil {
		return
	}
	present := make(map[target.ID]struct{}, len(snaps))
	for _, sn := range snaps {
		if sn.TargetID != "" {
			present[sn.TargetID] = struct{}{}
		}
	}
	loaded, err := s.store.ListTabs()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("list tab records for prune failed", "err", err)
		}
		return
	}
	for _, rec := range loaded {
		tid := target.ID(rec.TargetID)
		if _, ok := present[tid]; !ok {
			s.forgetTabManaged(tid)
		}
	}
}

func (s *Server) reconcileIdleFromDisk(snaps []state.TabSnapshot) {
	if s.tabIdle == nil || s.store == nil {
		return
	}
	loaded, err := s.store.ListTabs()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("load tab records failed", "err", err)
		}
		loaded = nil
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
	}
	for _, rec := range loaded {
		tid := target.ID(rec.TargetID)
		if _, ok := present[tid]; !ok {
			continue
		}
		effective := rec.LastActivityAt
		if effective.Before(minLast) {
			effective = minLast
		}
		s.tabIdle.MarkManagedAt(tid, effective)
	}
	s.syncTabIdlePresence(snaps)
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
	s.forgetTabManaged(tid)
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
			s.forgetTabManaged(tid)
			continue
		}
		if err := s.closeTabByShort(short); err != nil {
			if errors.Is(err, errTabCloseUnknownID) {
				s.forgetTabManaged(tid)
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
