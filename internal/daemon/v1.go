package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
	"github.com/yiplee/go-bb-browser/internal/state"
)

func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONRPCBytes(w, protocol.NullID, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(protocol.NullID, protocol.CodeParseError, "Parse error", nil)
		})
		return
	}

	if req.JSONRPC != "2.0" {
		id := req.ID
		if !protocol.RequestHasID(id) {
			id = protocol.NullID
		}
		s.writeJSONRPCBytes(w, id, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(id, protocol.CodeInvalidRequest, "invalid jsonrpc version", nil)
		})
		return
	}

	if !protocol.RequestHasID(req.ID) {
		s.writeJSONRPCBytes(w, protocol.NullID, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(protocol.NullID, protocol.CodeInvalidRequest, "missing id", nil)
		})
		return
	}

	id := req.ID
	params := protocol.NormalizeParams(req.Params)
	method := strings.TrimSpace(req.Method)

	if err := s.ensureBrowserSession(r.Context()); err != nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser not connected", &protocol.ErrData{
			Error: "could not connect to browser",
			Hint:  err.Error(),
		})
		return
	}

	switch method {
	case protocol.MethodTabList:
		s.handleTabList(w, id, params)
	case protocol.MethodTabFocus:
		s.handleTabFocus(w, id, params)
	case protocol.MethodTabSelect:
		s.handleTabSelect(w, id, params)
	case protocol.MethodTabNew:
		s.handleTabNew(w, id, params)
	case protocol.MethodGoto:
		s.handleGoto(w, id, params)
	case protocol.MethodReload:
		s.handleReload(w, id, params)
	case protocol.MethodTabClose:
		s.handleTabClose(w, id, params)
	case protocol.MethodScreenshot:
		s.handleScreenshot(w, id, params)
	case protocol.MethodEval:
		s.handleEval(w, id, params)
	case protocol.MethodClick:
		s.handleClick(w, id, params)
	case protocol.MethodFill:
		s.handleFill(w, id, params)
	case protocol.MethodNetwork:
		s.handleNetwork(w, id, params)
	case protocol.MethodConsole:
		s.handleConsole(w, id, params)
	case protocol.MethodErrors:
		s.handleErrors(w, id, params)
	case protocol.MethodFetch:
		s.handleFetch(w, id, params)
	case protocol.MethodSnapshot:
		s.handleSnapshot(w, id, params)
	case protocol.MethodNetworkRoute:
		s.handleNetworkRoute(w, id, params)
	case protocol.MethodNetworkUnroute:
		s.handleNetworkUnroute(w, id, params)
	case protocol.MethodNetworkClear:
		s.handleNetworkClear(w, id, params)
	case protocol.MethodConsoleClear:
		s.handleConsoleClear(w, id, params)
	case protocol.MethodErrorsClear:
		s.handleErrorsClear(w, id, params)
	default:
		if method == "" {
			s.rpcErr(w, id, protocol.CodeInvalidRequest, "missing method", nil)
			return
		}
		s.rpcErr(w, id, protocol.CodeMethodNotFound, "method not found", &protocol.ErrData{
			Error:  "unknown method",
			Method: method,
		})
	}
}

func (s *Server) writeJSONRPCBytes(w http.ResponseWriter, id json.RawMessage, build func() ([]byte, error)) {
	b, err := build()
	if err != nil {
		s.logger.Error("json-rpc marshal failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(b); err != nil && s.logger != nil {
		s.logger.Error("json-rpc write failed", "err", err)
	}
}

func (s *Server) rpcErr(w http.ResponseWriter, id json.RawMessage, code int, msg string, data *protocol.ErrData) {
	s.writeJSONRPCBytes(w, id, func() ([]byte, error) {
		return protocol.MarshalErrorResponse(id, code, msg, data)
	})
}

func (s *Server) rpcOK(w http.ResponseWriter, id json.RawMessage, result any) {
	s.writeJSONRPCBytes(w, id, func() ([]byte, error) {
		return protocol.MarshalResponse(id, result)
	})
}

func (s *Server) handleTabList(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabListParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	_ = p

	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := conn.PageTargets()
	if err != nil {
		s.logger.Error("tab_list targets failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	snaps := s.tabs.SyncPageTargets(targets)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ShortID < snaps[j].ShortID
	})
	s.syncRegistryFocusFromBrowser(conn, snaps)
	items := make([]protocol.TabListItem, 0, len(snaps))
	for _, sn := range snaps {
		items = append(items, protocol.TabListItem{
			Tab:   sn.ShortID,
			Title: sn.Title,
			URL:   sn.URL,
		})
	}
	focus := s.tabs.Selected()
	tabField := operationalTabShort(s.tabs, snaps)
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabListResult{
		Tab:   tabField,
		Seq:   seq,
		Tabs:  items,
		Focus: focus,
	})
	s.syncObservation(conn, targets)
}

func (s *Server) handleTabFocus(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabFocusParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	_ = p

	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := conn.PageTargets()
	if err != nil {
		s.logger.Error("tab_focus targets failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	snaps := s.tabs.SyncPageTargets(targets)
	if len(snaps) == 0 {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "no focused tab", &protocol.ErrData{
			Error:  "no page tabs",
			Hint:   "open a tab in Chrome or attach to a session with at least one page target",
			Method: protocol.MethodTabFocus,
		})
		return
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ShortID < snaps[j].ShortID
	})
	s.syncRegistryFocusFromBrowser(conn, snaps)
	focus := s.tabs.Selected()
	tabField := operationalTabShort(s.tabs, snaps)
	var title, url string
	for _, sn := range snaps {
		if sn.ShortID == tabField {
			title = sn.Title
			url = sn.URL
			break
		}
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabFocusResult{
		Tab:   tabField,
		Focus: focus,
		Title: title,
		URL:   url,
		Seq:   seq,
	})
	s.syncObservation(conn, targets)
}

// syncRegistryFocusFromBrowser updates TabRegistry selection when the browser
// has exactly one page with document.visibilityState === "visible" (typical
// single-window foreground tab after the user switches tabs in Chrome).
func (s *Server) syncRegistryFocusFromBrowser(conn tabConn, snaps []state.TabSnapshot) {
	if conn == nil || len(snaps) == 0 {
		return
	}
	type foregroundDetector interface {
		DetectForegroundShort(snaps []state.TabSnapshot) (short string, ok bool)
	}
	d, ok := conn.(foregroundDetector)
	if !ok {
		return
	}
	sh, ok := d.DetectForegroundShort(snaps)
	if !ok {
		return
	}
	_ = s.tabs.Select(sh)
}

func operationalTabShort(reg *state.TabRegistry, snaps []state.TabSnapshot) string {
	if len(snaps) == 0 {
		return ""
	}
	focus := reg.Selected()
	if focus != "" {
		for _, sn := range snaps {
			if sn.ShortID == focus {
				return focus
			}
		}
	}
	return snaps[0].ShortID
}

func (s *Server) handleTabSelect(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabSelectParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `tab_select requires "tab"`,
			Method: protocol.MethodTabSelect,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := conn.PageTargets()
	if err != nil {
		s.logger.Error("tab_select targets failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	s.tabs.SyncPageTargets(targets)

	if !s.tabs.Select(tab) {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodTabSelect,
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabSelectResult{Tab: tab, Seq: seq})
}

func (s *Server) handleTabNew(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabNewParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	initial := strings.TrimSpace(p.URL)
	tid, err := conn.CreatePageTarget(initial)
	if err != nil {
		s.logger.Error("tab_new create target failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "failed to create tab", &protocol.ErrData{
			Error: "failed to create tab",
			Hint:  s.cdpHint(err),
		})
		return
	}
	short := s.tabs.RegisterPageTarget(tid)
	s.tabs.Select(short)
	targets, ptErr := conn.PageTargets()
	if ptErr == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabNewResult{Tab: short, Seq: seq})
	if ptErr == nil {
		s.syncObservation(conn, targets)
	}
}

func (s *Server) handleGoto(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.GotoParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `goto requires "tab" and "url"`,
			Method: protocol.MethodGoto,
		})
		return
	}
	urlStr := strings.TrimSpace(p.URL)
	if urlStr == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing url", &protocol.ErrData{
			Error:  "missing url",
			Hint:   `goto requires "url"`,
			Method: protocol.MethodGoto,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.tabs.SyncPageTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodGoto,
		})
		return
	}
	if err := conn.Navigate(tid, urlStr); err != nil {
		s.logger.Error("goto navigate failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "navigation failed", &protocol.ErrData{
			Error: "navigation failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.GotoResult{Tab: tab, Seq: seq})
}

func (s *Server) handleReload(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ReloadParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `reload requires "tab"`,
			Method: protocol.MethodReload,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.tabs.SyncPageTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodReload,
		})
		return
	}
	if err := conn.Reload(tid); err != nil {
		s.logger.Error("reload failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "reload failed", &protocol.ErrData{
			Error: "reload failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ReloadResult{Tab: tab, Seq: seq})
}

func (s *Server) handleTabClose(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabCloseParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `tab_close requires "tab"`,
			Method: protocol.MethodTabClose,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.tabs.Lookup(tab)
	if !ok {
		if targets, err := conn.PageTargets(); err == nil {
			s.tabs.SyncPageTargets(targets)
			tid, ok = s.tabs.Lookup(tab)
		}
	}
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodTabClose,
		})
		return
	}
	if err := conn.CloseTarget(tid); err != nil {
		s.logger.Error("tab_close failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "failed to close tab", &protocol.ErrData{
			Error: "failed to close tab",
			Hint:  s.cdpHint(err),
		})
		return
	}
	targets, ptErr := conn.PageTargets()
	if ptErr == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabCloseResult{Tab: tab, Seq: seq})
	if ptErr == nil {
		s.syncObservation(conn, targets)
	}
}

func (s *Server) handleScreenshot(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ScreenshotParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodScreenshot,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodScreenshot,
		})
		return
	}
	raw, mime, err := conn.Screenshot(tid, p.Format)
	if err != nil {
		s.logger.Error("screenshot failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "screenshot failed", &protocol.ErrData{
			Error: "screenshot failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ScreenshotResult{
		Tab:  tab,
		Seq:  seq,
		Data: base64.StdEncoding.EncodeToString(raw),
		MIME: mime,
	})
}

func (s *Server) handleEval(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.EvalParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	script := strings.TrimSpace(p.Script)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodEval,
		})
		return
	}
	if script == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing script", &protocol.ErrData{
			Error:  "missing script",
			Method: protocol.MethodEval,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodEval,
		})
		return
	}
	out, err := conn.Eval(tid, script)
	if err != nil {
		s.logger.Error("eval failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "eval failed", &protocol.ErrData{
			Error: "eval failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.EvalResult{Tab: tab, Seq: seq, Result: out})
}

func (s *Server) handleClick(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ClickParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	sel := strings.TrimSpace(p.Selector)
	ref := strings.TrimSpace(p.Ref)
	if ref != "" {
		ref = strings.TrimPrefix(ref, "@")
		sel = fmt.Sprintf(`[__bb_snap_ref="%s"]`, strings.ReplaceAll(ref, `"`, `\"`))
	}
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodClick,
		})
		return
	}
	if sel == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector or ref",
			Method: protocol.MethodClick,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodClick,
		})
		return
	}
	if err := conn.Click(tid, sel); err != nil {
		s.logger.Error("click failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "click failed", &protocol.ErrData{
			Error: "click failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ClickResult{Tab: tab, Seq: seq})
}

func obsSince(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Server) handleNetwork(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(w, id, params, protocol.MethodNetwork, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryNetwork(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleConsole(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(w, id, params, protocol.MethodConsole, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryConsole(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleErrors(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(w, id, params, protocol.MethodErrors, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
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

func (s *Server) handleObsQuery(w http.ResponseWriter, id json.RawMessage, params json.RawMessage, method string, q func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64)) {
	var p protocol.ObsQueryParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: method,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: method,
		})
		return
	}
	since := obsSince(p.Since)
	events, cursor, dropped := q(tid, since)
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ObsQueryResult{
		Tab:     tab,
		Seq:     seq,
		Cursor:  cursor,
		Events:  events,
		Dropped: dropped,
	})
}

func (s *Server) handleFetch(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.FetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	rawURL := strings.TrimSpace(p.URL)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodFetch,
		})
		return
	}
	if rawURL == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing url", &protocol.ErrData{
			Error:  "missing url",
			Method: protocol.MethodFetch,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodFetch,
		})
		return
	}
	routing, ok := conn.(fetchConn)
	if !ok {
		s.rpcErr(w, id, protocol.CodeServerError, "fetch not supported", &protocol.ErrData{
			Error: "browser backend does not implement fetch",
		})
		return
	}
	hdrs := []byte(strings.TrimSpace(p.Headers))
	out, err := routing.FetchPage(tid, rawURL, p.Method, hdrs, p.Body)
	if err != nil {
		s.logger.Error("fetch failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "fetch failed", &protocol.ErrData{
			Error: "fetch failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	var wrap struct {
		OK     bool `json:"ok"`
		Status int  `json:"status"`
	}
	_ = json.Unmarshal(out, &wrap)
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.FetchResult{
		Tab:    tab,
		Seq:    seq,
		OK:     wrap.OK,
		Status: wrap.Status,
		Result: out,
	})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.SnapshotParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodSnapshot,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Method: protocol.MethodSnapshot,
		})
		return
	}
	snapper, ok := conn.(snapshotConn)
	if !ok {
		s.rpcErr(w, id, protocol.CodeServerError, "snapshot not supported", nil)
		return
	}
	title, pageURL, text, refs, err := snapper.Snapshot(tid, browser.SnapshotOpts{
		InteractiveOnly: p.InteractiveOnly,
		PruneEmpty:      p.PruneEmpty,
		MaxDepth:        p.MaxDepth,
		SelectorScope:   strings.TrimSpace(p.SelectorScope),
	})
	if err != nil {
		s.logger.Error("snapshot failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "snapshot failed", &protocol.ErrData{
			Error: "snapshot failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	if refs == nil {
		refs = map[string]string{}
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.SnapshotResult{
		Tab:   tab,
		Seq:   seq,
		Title: title,
		URL:   pageURL,
		Text:  text,
		Refs:  refs,
	})
}

func (s *Server) handleNetworkRoute(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkRouteParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	pat := strings.TrimSpace(p.URLPattern)
	if tab == "" || pat == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab or url_pattern", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	rc, ok := conn.(routeConn)
	if !ok {
		s.rpcErr(w, id, protocol.CodeServerError, "network_route not supported", nil)
		return
	}
	rule := browser.NetworkRouteRule{
		URLPattern:  pat,
		Abort:       p.Abort,
		MockBody:    p.Body,
		ContentType: p.ContentType,
		Status:      p.Status,
	}
	if err := rc.AppendNetworkRoute(tid, rule); err != nil {
		s.logger.Error("network_route failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "network_route failed", &protocol.ErrData{Hint: s.cdpHint(err)})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.NetworkRouteResult{
		Tab:    tab,
		Seq:    seq,
		Routes: rc.NetworkRouteCount(tid),
	})
}

func (s *Server) handleNetworkUnroute(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkUnrouteParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	rc, ok := conn.(routeConn)
	if !ok {
		s.rpcErr(w, id, protocol.CodeServerError, "network_unroute not supported", nil)
		return
	}
	if err := rc.RemoveNetworkRoutes(tid, p.URLPattern); err != nil {
		s.rpcErr(w, id, protocol.CodeServerError, "network_unroute failed", &protocol.ErrData{Hint: s.cdpHint(err)})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.NetworkUnrouteResult{
		Tab:    tab,
		Seq:    seq,
		Routes: rc.NetworkRouteCount(tid),
	})
}

func (s *Server) handleConsoleClear(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ConsoleClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearConsoleOnly(tid)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ConsoleClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleErrorsClear(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ErrorsClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearErrorsOnly(tid)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.ErrorsClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleNetworkClear(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearNetworkOnly(tid)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.NetworkClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleFill(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.FillParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	sel := strings.TrimSpace(p.Selector)
	ref := strings.TrimSpace(p.Ref)
	if ref != "" {
		ref = strings.TrimPrefix(ref, "@")
		sel = fmt.Sprintf(`[__bb_snap_ref="%s"]`, strings.ReplaceAll(ref, `"`, `\"`))
	}
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodFill,
		})
		return
	}
	if sel == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector or ref",
			Method: protocol.MethodFill,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	unlock := s.lockTab(tab)
	defer unlock()
	tid, ok := s.resolveTab(conn, tab)
	if !ok {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodFill,
		})
		return
	}
	if err := conn.Fill(tid, sel, p.Text); err != nil {
		s.logger.Error("fill failed", "err", err)
		s.rpcErr(w, id, protocol.CodeServerError, "fill failed", &protocol.ErrData{
			Error: "fill failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.FillResult{Tab: tab, Seq: seq})
}

func (s *Server) resolveTab(conn tabConn, tab string) (target.ID, bool) {
	tid, ok := s.tabs.Lookup(tab)
	if ok {
		return tid, true
	}
	if targets, err := conn.PageTargets(); err == nil {
		s.tabs.SyncPageTargets(targets)
		return s.tabs.Lookup(tab)
	}
	return tid, false
}

func (s *Server) cdpHint(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// tabConn is the CDP surface used by /v1 handlers.
type tabConn interface {
	PageTargets() ([]*target.Info, error)
	CreatePageTarget(initialURL string) (target.ID, error)
	CloseTarget(id target.ID) error
	Navigate(tabID target.ID, url string) error
	Reload(tabID target.ID) error
	Screenshot(tabID target.ID, format string) ([]byte, string, error)
	Eval(tabID target.ID, script string) (json.RawMessage, error)
	Click(tabID target.ID, selector string) error
	Fill(tabID target.ID, selector, text string) error
}

type fetchConn interface {
	FetchPage(tabID target.ID, rawURL, method string, headersJSON []byte, body string) (json.RawMessage, error)
}

type snapshotConn interface {
	Snapshot(tabID target.ID, opts browser.SnapshotOpts) (title, pageURL, text string, refs map[string]string, err error)
}

type routeConn interface {
	AppendNetworkRoute(tabID target.ID, rule browser.NetworkRouteRule) error
	RemoveNetworkRoutes(tabID target.ID, urlPattern string) error
	NetworkRouteCount(tabID target.ID) int
}

// lockTab serializes CDP operations per short tab id (IMPLEMENTATION_PLAN §8.2).
func (s *Server) lockTab(short string) func() {
	if short == "" {
		return func() {}
	}
	s.tabMuOps.Lock()
	if s.tabCDPLocks == nil {
		s.tabCDPLocks = make(map[string]*sync.Mutex)
	}
	m, ok := s.tabCDPLocks[short]
	if !ok {
		m = &sync.Mutex{}
		s.tabCDPLocks[short] = m
	}
	s.tabMuOps.Unlock()
	m.Lock()
	return m.Unlock
}
