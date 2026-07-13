package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
)

var (
	errTabCloseNoConn    = errors.New("browser session not ready")
	errTabCloseUnknownID = errors.New("unknown tab id")
	errTabLockTimeout    = fmt.Errorf("tab lock timeout: %w", context.DeadlineExceeded)
)

func (s *Server) markTabManaged(tid target.ID, short, openURL string, silent bool) {
	if s.tabIdle != nil {
		s.tabIdle.MarkManaged(tid)
	}
}

func (s *Server) touchTabActivity(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.Touch(tid)
	}
}

func (s *Server) forgetTabManaged(tid target.ID) {
	if s.tabIdle != nil {
		s.tabIdle.Forget(tid)
	}
}

func (s *Server) syncTabsFromTargets(infos []*target.Info) []state.TabSnapshot {
	snaps, removed := s.tabs.SyncPageTargetsDetailed(infos)
	for _, tab := range removed {
		s.clearRemovedTarget(tab.TargetID, tab.ShortID)
	}
	s.syncTabIdlePresence(snaps)
	return snaps
}

func (s *Server) clearRemovedTarget(tid target.ID, short string) {
	if s.obsStore != nil {
		s.obsStore.ClearTarget(tid)
	}
	s.forgetTabManaged(tid)
	s.forgetTabLock(short)
	if sess, ok := s.tabConn().(*browser.Session); ok {
		sess.ForgetTarget(tid)
	}
}

func (s *Server) removeTargetState(tid target.ID) {
	short, ok := s.tabs.RemoveTarget(tid)
	if !ok {
		return
	}
	s.clearRemovedTarget(tid, short)
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

func (s *Server) reconcileIdleFromLog(snaps []state.TabSnapshot) {
	if s.tabIdle == nil || s.store == nil {
		return
	}
	loaded := s.store.ReplayManagedTabActivity()

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
	for shortID, lastAt := range loaded {
		tid, ok := s.tabs.Lookup(shortID)
		if !ok {
			continue
		}
		if _, ok := present[tid]; !ok {
			continue
		}
		effective := lastAt
		if effective.Before(minLast) {
			effective = minLast
		}
		s.tabIdle.MarkManagedIfAbsentAt(tid, effective)
	}
}

func (s *Server) closeTabByShort(ctx context.Context, tab string) error {
	conn := s.tabConn()
	if conn == nil {
		return errTabCloseNoConn
	}
	unlock, ok := s.lockTab(ctx, tab)
	if !ok {
		return errTabLockTimeout
	}
	defer unlock()

	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := pageTargets(ctx, conn); err == nil {
			s.syncTabsFromTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		return errTabCloseUnknownID
	}
	if err := closeTarget(ctx, conn, tid); err != nil {
		return err
	}
	// Target.closeTarget succeeded, so clear every target-scoped resource even if
	// no later lifecycle event is delivered.
	s.removeTargetState(tid)
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
				s.logger.Warn("tab idle cleanup skipped: browser supervisor not ready", "err", err)
			}
			return
		}
		short, ok := s.tabs.ShortForTarget(tid)
		if !ok {
			s.forgetTabManaged(tid)
			continue
		}
		if err := s.closeTabByShort(ctx, short); err != nil {
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
