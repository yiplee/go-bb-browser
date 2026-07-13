package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func obsSince(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Server) handleNetwork(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodNetwork, browser.ObservationNetwork, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryNetwork(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleConsole(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodConsole, browser.ObservationConsole, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryConsole(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleErrors(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodErrors, browser.ObservationErrors, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryErrors(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func mapObsEvents(in []state.ObsEvent) []protocol.ObsEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.ObsEvent, len(in))
	for i, e := range in {
		out[i] = protocol.ObsEvent{Seq: e.Seq, Data: e.Data}
	}
	return out
}

func (s *Server) handleObsQuery(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage, method string, kind browser.ObservationKind, q func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64)) {
	var p protocol.ObsQueryParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{Error: "missing tab", Method: method})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock, ok := s.lockTab(ctx, tab)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "request timed out", tabLockTimeoutData())
		return
	}
	defer unlock()
	tid, ok := s.resolveTab(ctx, conn, tab)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{Error: "unknown tab id", Hint: "invalid or stale tab short id", Method: method})
		return
	}
	if sess, ok := conn.(*browser.Session); ok && s.obsSink != nil {
		if err := sess.EnsureObservation(ctx, tid, kind, s.obsSink, s.logger); err != nil {
			s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to enable observation", &protocol.ErrData{Error: "failed to enable observation", Hint: s.cdpHint(err)})
			return
		}
	}
	since := obsSince(p.Since)
	events, cursor, dropped := q(tid, since)
	if sess, ok := conn.(*browser.Session); ok {
		stats := sess.ObservationStats(tid, kind)
		if stats.Cursor > cursor {
			cursor = stats.Cursor
		}
		dropped += stats.Dropped
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ObsQueryResult{Tab: tab, Seq: seq, Cursor: cursor, Events: events, Dropped: dropped})
}
