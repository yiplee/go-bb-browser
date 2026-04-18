package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/protocol"
)

func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeV1Error(w, http.StatusMethodNotAllowed, protocol.V1Error{
			Error:  "method not allowed",
			Action: "",
		})
		return
	}
	var req protocol.V1Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error: "invalid JSON body",
			Hint:  err.Error(),
		})
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	if req.Action == "" {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error: "missing action",
		})
		return
	}

	switch req.Action {
	case protocol.ActionTabList:
		s.handleTabList(w, req)
	case protocol.ActionTabSelect:
		s.handleTabSelect(w, req)
	case protocol.ActionTabNew:
		s.handleTabNew(w, req)
	case protocol.ActionGoto:
		s.handleGoto(w, req)
	case protocol.ActionTabClose:
		s.handleTabClose(w, req)
	default:
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "unknown action",
			Action: req.Action,
		})
	}
}

func (s *Server) handleTabList(w http.ResponseWriter, _ protocol.V1Request) {
	conn := s.tabConn()
	if conn == nil {
		s.writeV1Error(w, http.StatusServiceUnavailable, protocol.V1Error{
			Error: "browser session not ready",
		})
		return
	}
	targets, err := conn.PageTargets()
	if err != nil {
		s.logger.Error("tab_list targets failed", "err", err)
		s.writeV1Error(w, http.StatusBadGateway, protocol.V1Error{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	snaps := s.tabs.SyncPageTargets(targets)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ShortID < snaps[j].ShortID
	})
	items := make([]protocol.TabListItem, 0, len(snaps))
	for _, sn := range snaps {
		items = append(items, protocol.TabListItem{
			Tab:   sn.ShortID,
			Title: sn.Title,
			URL:   sn.URL,
		})
	}
	focus := s.tabs.Selected()
	var tabField string
	if focus != "" {
		for _, sn := range snaps {
			if sn.ShortID == focus {
				tabField = focus
				break
			}
		}
	}
	if tabField == "" && len(snaps) > 0 {
		tabField = snaps[0].ShortID
	}
	seq := s.seq.Next()
	body := protocol.TabListOK{
		Tab:   tabField,
		Seq:   seq,
		Tabs:  items,
		Focus: focus,
	}
	b, err := protocol.MarshalTabList(body)
	if err != nil {
		s.logger.Error("tab_list marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("tab_list write failed", "err", err)
	}
}

func (s *Server) handleTabSelect(w http.ResponseWriter, req protocol.V1Request) {
	tab := strings.TrimSpace(req.Tab)
	if tab == "" {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "missing tab",
			Action: req.Action,
			Hint:   "tab_select requires \"tab\"",
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.writeV1Error(w, http.StatusServiceUnavailable, protocol.V1Error{
			Error: "browser session not ready",
		})
		return
	}
	targets, err := conn.PageTargets()
	if err != nil {
		s.logger.Error("tab_select targets failed", "err", err)
		s.writeV1Error(w, http.StatusBadGateway, protocol.V1Error{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	s.tabs.SyncPageTargets(targets)

	if !s.tabs.Select(tab) {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "unknown tab id",
			Action: req.Action,
			Hint:   "invalid or stale tab short id",
		})
		return
	}
	seq := s.seq.Next()
	b, err := protocol.MarshalTabSelect(protocol.TabSelectOK{Tab: tab, Seq: seq})
	if err != nil {
		s.logger.Error("tab_select marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("tab_select write failed", "err", err)
	}
}

func (s *Server) handleTabNew(w http.ResponseWriter, req protocol.V1Request) {
	conn := s.tabConn()
	if conn == nil {
		s.writeV1Error(w, http.StatusServiceUnavailable, protocol.V1Error{
			Error: "browser session not ready",
		})
		return
	}
	initial := strings.TrimSpace(req.URL)
	id, err := conn.CreatePageTarget(initial)
	if err != nil {
		s.logger.Error("tab_new create target failed", "err", err)
		s.writeV1Error(w, http.StatusBadGateway, protocol.V1Error{
			Error: "failed to create tab",
			Hint:  s.cdpHint(err),
		})
		return
	}
	short := s.tabs.RegisterPageTarget(id)
	s.tabs.Select(short)
	if targets, err := conn.PageTargets(); err == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	b, err := protocol.MarshalTabNew(protocol.TabNewOK{Tab: short, Seq: seq})
	if err != nil {
		s.logger.Error("tab_new marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("tab_new write failed", "err", err)
	}
}

func (s *Server) handleGoto(w http.ResponseWriter, req protocol.V1Request) {
	tab := strings.TrimSpace(req.Tab)
	if tab == "" {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "missing tab",
			Action: req.Action,
			Hint:   "goto requires \"tab\" and \"url\"",
		})
		return
	}
	urlStr := strings.TrimSpace(req.URL)
	if urlStr == "" {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "missing url",
			Action: req.Action,
			Hint:   "goto requires \"url\"",
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.writeV1Error(w, http.StatusServiceUnavailable, protocol.V1Error{
			Error: "browser session not ready",
		})
		return
	}
	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.tabs.SyncPageTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "unknown tab id",
			Action: req.Action,
			Hint:   "invalid or stale tab short id",
		})
		return
	}
	if err := conn.Navigate(tid, urlStr); err != nil {
		s.logger.Error("goto navigate failed", "err", err)
		s.writeV1Error(w, http.StatusBadGateway, protocol.V1Error{
			Error: "navigation failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	b, err := protocol.MarshalGoto(protocol.GotoOK{Tab: tab, Seq: seq})
	if err != nil {
		s.logger.Error("goto marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("goto write failed", "err", err)
	}
}

func (s *Server) handleTabClose(w http.ResponseWriter, req protocol.V1Request) {
	tab := strings.TrimSpace(req.Tab)
	if tab == "" {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "missing tab",
			Action: req.Action,
			Hint:   "tab_close requires \"tab\"",
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.writeV1Error(w, http.StatusServiceUnavailable, protocol.V1Error{
			Error: "browser session not ready",
		})
		return
	}
	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.tabs.SyncPageTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		s.writeV1Error(w, http.StatusBadRequest, protocol.V1Error{
			Error:  "unknown tab id",
			Action: req.Action,
			Hint:   "invalid or stale tab short id",
		})
		return
	}
	if err := conn.CloseTarget(tid); err != nil {
		s.logger.Error("tab_close failed", "err", err)
		s.writeV1Error(w, http.StatusBadGateway, protocol.V1Error{
			Error: "failed to close tab",
			Hint:  s.cdpHint(err),
		})
		return
	}
	if targets, err := conn.PageTargets(); err == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	b, err := protocol.MarshalTabClose(protocol.TabCloseOK{Tab: tab, Seq: seq})
	if err != nil {
		s.logger.Error("tab_close marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil {
		s.logger.Error("tab_close write failed", "err", err)
	}
}

func (s *Server) writeV1Error(w http.ResponseWriter, status int, e protocol.V1Error) {
	b, err := protocol.MarshalError(e)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(b); err != nil && s.logger != nil {
		s.logger.Error("error response write failed", "err", err)
	}
}

func (s *Server) cdpHint(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("CDP error (%T)", err)
}

// tabConn is the CDP surface used by /v1 handlers.
type tabConn interface {
	PageTargets() ([]*target.Info, error)
	CreatePageTarget(initialURL string) (target.ID, error)
	CloseTarget(id target.ID) error
	Navigate(tabID target.ID, url string) error
}
