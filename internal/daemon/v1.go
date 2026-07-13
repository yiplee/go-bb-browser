package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/internal/store"
	"github.com/yiplee/go-bb-browser/internal/timeout"
	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeJSONRPCBytes(ctx, w, protocol.NullID, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(protocol.NullID, protocol.CodeParseError, "Parse error", nil)
		})
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(rawBody, &req); err != nil {
		s.writeJSONRPCBytes(ctx, w, protocol.NullID, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(protocol.NullID, protocol.CodeParseError, "Parse error", nil)
		})
		return
	}

	if req.JSONRPC != "2.0" {
		id := req.ID
		if !protocol.RequestHasID(id) {
			id = protocol.NullID
		}
		s.writeJSONRPCBytes(ctx, w, id, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(id, protocol.CodeInvalidRequest, "invalid jsonrpc version", nil)
		})
		return
	}

	if !protocol.RequestHasID(req.ID) {
		s.writeJSONRPCBytes(ctx, w, protocol.NullID, func() ([]byte, error) {
			return protocol.MarshalErrorResponse(protocol.NullID, protocol.CodeInvalidRequest, "missing id", nil)
		})
		return
	}

	id := req.ID
	params := protocol.NormalizeParams(req.Params)
	method := strings.TrimSpace(req.Method)

	if !protocol.IsDispatchedMethod(method) {
		if method == "" {
			s.rpcErr(ctx, w, id, protocol.CodeInvalidRequest, "missing method", nil)
			return
		}
		s.rpcErr(ctx, w, id, protocol.CodeMethodNotFound, "method not found", &protocol.ErrData{
			Error:  "unknown method",
			Method: method,
		})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout.Operation)
	defer cancel()

	ctx = contextWithAudit(ctx, &auditMeta{
		action:   method,
		body:     json.RawMessage(rawBody),
		senderIP: store.ClientIP(r),
		at:       time.Now().UTC(),
	})

	if err := s.ensureBrowserSession(ctx); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser not connected", &protocol.ErrData{
			Error: "could not connect to browser",
			Hint:  err.Error(),
		})
		return
	}

	switch method {
	case protocol.MethodTabList:
		s.handleTabList(ctx, w, id, params)
	case protocol.MethodTabFocus:
		s.handleTabFocus(ctx, w, id, params)
	case protocol.MethodTabSelect:
		s.handleTabSelect(ctx, w, id, params)
	case protocol.MethodTabNew:
		s.handleTabNew(ctx, w, id, params)
	case protocol.MethodGoto:
		s.handleGoto(ctx, w, id, params)
	case protocol.MethodReload:
		s.handleReload(ctx, w, id, params)
	case protocol.MethodTabClose:
		s.handleTabClose(ctx, w, id, params)
	case protocol.MethodScreenshot:
		s.handleScreenshot(ctx, w, id, params)
	case protocol.MethodEval:
		s.handleEval(ctx, w, id, params)
	case protocol.MethodClick:
		s.handleClick(ctx, w, id, params)
	case protocol.MethodFill:
		s.handleFill(ctx, w, id, params)
	case protocol.MethodNetwork:
		s.handleNetwork(ctx, w, id, params)
	case protocol.MethodConsole:
		s.handleConsole(ctx, w, id, params)
	case protocol.MethodErrors:
		s.handleErrors(ctx, w, id, params)
	case protocol.MethodFetch:
		s.handleFetch(ctx, w, id, params)
	case protocol.MethodSnapshot:
		s.handleSnapshot(ctx, w, id, params)
	case protocol.MethodNetworkRoute:
		s.handleNetworkRoute(ctx, w, id, params)
	case protocol.MethodNetworkUnroute:
		s.handleNetworkUnroute(ctx, w, id, params)
	case protocol.MethodNetworkClear:
		s.handleNetworkClear(ctx, w, id, params)
	case protocol.MethodConsoleClear:
		s.handleConsoleClear(ctx, w, id, params)
	case protocol.MethodErrorsClear:
		s.handleErrorsClear(ctx, w, id, params)
	}
}

func (s *Server) writeJSONRPCBytes(ctx context.Context, w http.ResponseWriter, id json.RawMessage, build func() ([]byte, error)) {
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
	s.scheduleAudit(ctx, b)
}

func (s *Server) scheduleAudit(ctx context.Context, resp []byte) {
	meta := auditMetaFrom(ctx)
	if meta == nil || s.store == nil {
		return
	}
	tab, seq, ok, errMsg := store.ParseResponseSummary(resp)
	if tab == "" {
		tab = store.TabFromRequestBody(meta.body)
	}
	rec := store.LogRecord{
		Action:   meta.action,
		Body:     meta.body,
		Tab:      tab,
		SenderIP: meta.senderIP,
		Seq:      seq,
		OK:       ok,
		Error:    errMsg,
		Time:     meta.at,
	}
	action := meta.action
	s.auditWG.Add(1)
	go func() {
		defer s.auditWG.Done()
		if err := s.store.AppendRPC(rec); err != nil && s.logger != nil {
			s.logger.Warn("append rpc log failed", "err", err, "action", action)
		}
	}()
}

func (s *Server) rpcErr(ctx context.Context, w http.ResponseWriter, id json.RawMessage, code int, msg string, data *protocol.ErrData) {
	s.writeJSONRPCBytes(ctx, w, id, func() ([]byte, error) {
		return protocol.MarshalErrorResponse(id, code, msg, data)
	})
}

func (s *Server) rpcOK(ctx context.Context, w http.ResponseWriter, id json.RawMessage, result any) {
	s.writeJSONRPCBytes(ctx, w, id, func() ([]byte, error) {
		return protocol.MarshalResponse(id, result)
	})
}

func (s *Server) nextSeq(ctx context.Context, w http.ResponseWriter, id json.RawMessage) (uint64, bool) {
	seq, err := s.store.NextSeq()
	if err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInternalError, "sequence error", nil)
		return 0, false
	}
	return seq, true
}

func (s *Server) handleTabList(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabListParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	_ = p

	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := pageTargets(ctx, conn)
	if err != nil {
		s.logger.Error("tab_list targets failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	snaps := s.syncTabsFromTargets(targets)
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ShortID < snaps[j].ShortID
	})
	s.syncRegistryFocusFromBrowser(ctx, conn, snaps)
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
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.TabListResult{
		Tab:   tabField,
		Seq:   seq,
		Tabs:  items,
		Focus: focus,
	})
	s.syncObservation(conn, targets)
}

func (s *Server) handleTabFocus(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabFocusParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	_ = p

	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := pageTargets(ctx, conn)
	if err != nil {
		s.logger.Error("tab_focus targets failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	snaps := s.syncTabsFromTargets(targets)
	if len(snaps) == 0 {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "no focused tab", &protocol.ErrData{
			Error:  "no page tabs",
			Hint:   "open a tab in Chrome or attach to a session with at least one page target",
			Method: protocol.MethodTabFocus,
		})
		return
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].ShortID < snaps[j].ShortID
	})
	s.syncRegistryFocusFromBrowser(ctx, conn, snaps)
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
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.TabFocusResult{
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
func (s *Server) syncRegistryFocusFromBrowser(ctx context.Context, conn tabConn, snaps []state.TabSnapshot) {
	if conn == nil || len(snaps) == 0 {
		return
	}
	type foregroundDetector interface {
		DetectForegroundShort(snaps []state.TabSnapshot) (short string, ok bool)
	}
	type foregroundDetectorContext interface {
		DetectForegroundShortContext(context.Context, []state.TabSnapshot) (short string, ok bool)
	}
	if d, ok := conn.(foregroundDetectorContext); ok {
		sh, ok := d.DetectForegroundShortContext(ctx, snaps)
		if ok {
			_ = s.tabs.Select(sh)
		}
		return
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

func (s *Server) handleTabSelect(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabSelectParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `tab_select requires "tab"`,
			Method: protocol.MethodTabSelect,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	targets, err := pageTargets(ctx, conn)
	if err != nil {
		s.logger.Error("tab_select targets failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to list targets", &protocol.ErrData{
			Error: "failed to list targets",
			Hint:  s.cdpHint(err),
		})
		return
	}
	s.syncTabsFromTargets(targets)

	if !s.tabs.Select(tab) {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodTabSelect,
		})
		return
	}
	if tid, ok := s.tabs.Lookup(tab); ok {
		s.touchTabActivity(tid)
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.TabSelectResult{Tab: tab, Seq: seq})
}

func (s *Server) handleTabNew(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabNewParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	initial := strings.TrimSpace(p.URL)
	tid, err := createPageTarget(ctx, conn, initial, p.Silent)
	if err != nil {
		s.logger.Error("tab_new create target failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to create tab", &protocol.ErrData{
			Error: "failed to create tab",
			Hint:  s.cdpHint(err),
		})
		return
	}
	short := s.tabs.RegisterPageTarget(tid)
	s.markTabManaged(tid, short, initial, p.Silent)
	if !p.Silent {
		s.tabs.Select(short)
	}
	targets, ptErr := pageTargets(ctx, conn)
	if ptErr == nil {
		s.syncTabsFromTargets(targets)
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.TabNewResult{Tab: short, Seq: seq})
	if ptErr == nil {
		s.syncObservation(conn, targets)
	}
}

func (s *Server) handleGoto(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.GotoParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `goto requires "tab" and "url"`,
			Method: protocol.MethodGoto,
		})
		return
	}
	urlStr := strings.TrimSpace(p.URL)
	if urlStr == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing url", &protocol.ErrData{
			Error:  "missing url",
			Hint:   `goto requires "url"`,
			Method: protocol.MethodGoto,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodGoto,
		})
		return
	}
	if err := navigate(ctx, conn, tid, urlStr); err != nil {
		s.logger.Error("goto navigate failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "navigation failed", &protocol.ErrData{
			Error: "navigation failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.GotoResult{Tab: tab, Seq: seq})
}

func (s *Server) handleReload(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ReloadParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `reload requires "tab"`,
			Method: protocol.MethodReload,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodReload,
		})
		return
	}
	if err := reload(ctx, conn, tid); err != nil {
		s.logger.Error("reload failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "reload failed", &protocol.ErrData{
			Error: "reload failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ReloadResult{Tab: tab, Seq: seq})
}

func (s *Server) handleTabClose(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.TabCloseParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Hint:   `tab_close requires "tab"`,
			Method: protocol.MethodTabClose,
		})
		return
	}
	conn := s.tabConn()
	if conn == nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		return
	}
	if err := s.closeTabByShort(ctx, tab); err != nil {
		switch {
		case errors.Is(err, errTabCloseUnknownID):
			s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
				Error:  "unknown tab id",
				Hint:   "invalid or stale tab short id",
				Method: protocol.MethodTabClose,
			})
		case errors.Is(err, errTabCloseNoConn):
			s.rpcErr(ctx, w, id, protocol.CodeServerError, "browser session not ready", nil)
		case errors.Is(err, errTabLockTimeout):
			s.rpcErr(ctx, w, id, protocol.CodeServerError, "request timed out", tabLockTimeoutData())
		default:
			s.logger.Error("tab_close failed", "err", err)
			s.rpcErr(ctx, w, id, protocol.CodeServerError, "failed to close tab", &protocol.ErrData{
				Error: "failed to close tab",
				Hint:  s.cdpHint(err),
			})
		}
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.TabCloseResult{Tab: tab, Seq: seq})
}

func (s *Server) handleScreenshot(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ScreenshotParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodScreenshot,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodScreenshot,
		})
		return
	}
	raw, mime, err := screenshot(ctx, conn, tid, p.Format)
	if err != nil {
		s.logger.Error("screenshot failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "screenshot failed", &protocol.ErrData{
			Error: "screenshot failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ScreenshotResult{
		Tab:  tab,
		Seq:  seq,
		Data: base64.StdEncoding.EncodeToString(raw),
		MIME: mime,
	})
}

func (s *Server) handleEval(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.EvalParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	script := strings.TrimSpace(p.Script)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodEval,
		})
		return
	}
	if script == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing script", &protocol.ErrData{
			Error:  "missing script",
			Method: protocol.MethodEval,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodEval,
		})
		return
	}
	out, err := eval(ctx, conn, tid, script)
	if err != nil {
		s.logger.Error("eval failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "eval failed", &protocol.ErrData{
			Error: "eval failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.EvalResult{Tab: tab, Seq: seq, Result: out})
}

func (s *Server) handleClick(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ClickParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodClick,
		})
		return
	}
	if sel == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector or ref",
			Method: protocol.MethodClick,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodClick,
		})
		return
	}
	if err := click(ctx, conn, tid, sel); err != nil {
		s.logger.Error("click failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "click failed", &protocol.ErrData{
			Error: "click failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ClickResult{Tab: tab, Seq: seq})
}

func obsSince(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Server) handleNetwork(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodNetwork, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryNetwork(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleConsole(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodConsole, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
		ev, cur, drop := s.obsStore.QueryConsole(tid, since)
		return mapObsEvents(ev), cur, drop
	})
}

func (s *Server) handleErrors(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	s.handleObsQuery(ctx, w, id, params, protocol.MethodErrors, func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64) {
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

func (s *Server) handleObsQuery(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage, method string, q func(tid target.ID, since uint64) ([]protocol.ObsEvent, uint64, uint64)) {
	var p protocol.ObsQueryParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: method,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: method,
		})
		return
	}
	since := obsSince(p.Since)
	events, cursor, dropped := q(tid, since)
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ObsQueryResult{
		Tab:     tab,
		Seq:     seq,
		Cursor:  cursor,
		Events:  events,
		Dropped: dropped,
	})
}

func (s *Server) handleFetch(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.FetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	rawURL := strings.TrimSpace(p.URL)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodFetch,
		})
		return
	}
	if rawURL == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing url", &protocol.ErrData{
			Error:  "missing url",
			Method: protocol.MethodFetch,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodFetch,
		})
		return
	}
	routing, ok := conn.(fetchConn)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "fetch not supported", &protocol.ErrData{
			Error: "browser backend does not implement fetch",
		})
		return
	}
	hdrs := []byte(strings.TrimSpace(p.Headers))
	out, err := fetchPage(ctx, routing, tid, rawURL, p.Method, hdrs, p.Body)
	if err != nil {
		s.logger.Error("fetch failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "fetch failed", &protocol.ErrData{
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
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.FetchResult{
		Tab:    tab,
		Seq:    seq,
		OK:     wrap.OK,
		Status: wrap.Status,
		Result: out,
	})
}

func (s *Server) handleSnapshot(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.SnapshotParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodSnapshot,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Method: protocol.MethodSnapshot,
		})
		return
	}
	snapper, ok := conn.(snapshotConn)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "snapshot not supported", nil)
		return
	}
	title, pageURL, text, refs, err := snapshot(ctx, snapper, tid, browser.SnapshotOpts{
		InteractiveOnly: p.InteractiveOnly,
		PruneEmpty:      p.PruneEmpty,
		MaxDepth:        p.MaxDepth,
		SelectorScope:   strings.TrimSpace(p.SelectorScope),
	})
	if err != nil {
		s.logger.Error("snapshot failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "snapshot failed", &protocol.ErrData{
			Error: "snapshot failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	if refs == nil {
		refs = map[string]string{}
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.SnapshotResult{
		Tab:   tab,
		Seq:   seq,
		Title: title,
		URL:   pageURL,
		Text:  text,
		Refs:  refs,
	})
}

func (s *Server) handleNetworkRoute(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkRouteParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	pat := strings.TrimSpace(p.URLPattern)
	if tab == "" || pat == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab or url_pattern", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	rc, ok := conn.(routeConn)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "network_route not supported", nil)
		return
	}
	rule := browser.NetworkRouteRule{
		URLPattern:  pat,
		Abort:       p.Abort,
		MockBody:    p.Body,
		ContentType: p.ContentType,
		Status:      p.Status,
	}
	if err := appendNetworkRoute(ctx, rc, tid, rule); err != nil {
		s.logger.Error("network_route failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "network_route failed", &protocol.ErrData{Hint: s.cdpHint(err)})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.NetworkRouteResult{
		Tab:    tab,
		Seq:    seq,
		Routes: rc.NetworkRouteCount(tid),
	})
}

func (s *Server) handleNetworkUnroute(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkUnrouteParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	rc, ok := conn.(routeConn)
	if !ok {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "network_unroute not supported", nil)
		return
	}
	if err := removeNetworkRoutes(ctx, rc, tid, p.URLPattern); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "network_unroute failed", &protocol.ErrData{Hint: s.cdpHint(err)})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.NetworkUnrouteResult{
		Tab:    tab,
		Seq:    seq,
		Routes: rc.NetworkRouteCount(tid),
	})
}

func (s *Server) handleConsoleClear(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ConsoleClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearConsoleOnly(tid)
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ConsoleClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleErrorsClear(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.ErrorsClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearErrorsOnly(tid)
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.ErrorsClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleNetworkClear(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.NetworkClearParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	if tab == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", nil)
		return
	}
	if s.obsStore != nil {
		s.obsStore.ClearNetworkOnly(tid)
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.NetworkClearResult{Tab: tab, Seq: seq})
}

func (s *Server) handleFill(ctx context.Context, w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.FillParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "invalid params", nil)
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodFill,
		})
		return
	}
	if sel == "" {
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector or ref",
			Method: protocol.MethodFill,
		})
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
		s.rpcErr(ctx, w, id, protocol.CodeInvalidParams, "unknown tab id", &protocol.ErrData{
			Error:  "unknown tab id",
			Hint:   "invalid or stale tab short id",
			Method: protocol.MethodFill,
		})
		return
	}
	if err := fill(ctx, conn, tid, sel, p.Text); err != nil {
		s.logger.Error("fill failed", "err", err)
		s.rpcErr(ctx, w, id, protocol.CodeServerError, "fill failed", &protocol.ErrData{
			Error: "fill failed",
			Hint:  s.cdpHint(err),
		})
		return
	}
	seq, ok := s.nextSeq(ctx, w, id)
	if !ok {
		return
	}
	s.rpcOK(ctx, w, id, protocol.FillResult{Tab: tab, Seq: seq})
}

func (s *Server) resolveTab(ctx context.Context, conn tabConn, tab string) (target.ID, bool) {
	tid, ok := s.tabs.Lookup(tab)
	if ok {
		s.touchTabActivity(tid)
		return tid, true
	}
	if targets, err := pageTargets(ctx, conn); err == nil {
		s.syncTabsFromTargets(targets)
		if tid, ok := s.tabs.Lookup(tab); ok {
			s.touchTabActivity(tid)
			return tid, true
		}
	}
	return tid, false
}

func (s *Server) cdpHint(err error) string {
	if err == nil {
		return ""
	}
	if isCDPSessionLost(err) {
		s.fatalCDPLost(err)
	}
	return err.Error()
}

// tabConn is the CDP surface used by /v1 handlers.
type tabConn interface {
	PageTargets() ([]*target.Info, error)
	CreatePageTarget(initialURL string, silent bool) (target.ID, error)
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

type pageTargetsContextConn interface {
	PageTargetsContext(context.Context) ([]*target.Info, error)
}
type createPageTargetContextConn interface {
	CreatePageTargetContext(context.Context, string, bool) (target.ID, error)
}
type closeTargetContextConn interface {
	CloseTargetContext(context.Context, target.ID) error
}
type navigateContextConn interface {
	NavigateContext(context.Context, target.ID, string) error
}
type reloadContextConn interface {
	ReloadContext(context.Context, target.ID) error
}
type screenshotContextConn interface {
	ScreenshotContext(context.Context, target.ID, string) ([]byte, string, error)
}
type evalContextConn interface {
	EvalContext(context.Context, target.ID, string) (json.RawMessage, error)
}
type clickContextConn interface {
	ClickContext(context.Context, target.ID, string) error
}
type fillContextConn interface {
	FillContext(context.Context, target.ID, string, string) error
}
type fetchContextConn interface {
	FetchPageContext(context.Context, target.ID, string, string, []byte, string) (json.RawMessage, error)
}
type snapshotContextConn interface {
	SnapshotContext(context.Context, target.ID, browser.SnapshotOpts) (string, string, string, map[string]string, error)
}
type appendNetworkRouteContextConn interface {
	AppendNetworkRouteContext(context.Context, target.ID, browser.NetworkRouteRule) error
}
type removeNetworkRoutesContextConn interface {
	RemoveNetworkRoutesContext(context.Context, target.ID, string) error
}

func tabLockTimeoutData() *protocol.ErrData {
	return &protocol.ErrData{
		Error: "tab operation lock deadline exceeded",
		Hint:  "another operation is still running for this tab",
	}
}

func pageTargets(ctx context.Context, conn tabConn) ([]*target.Info, error) {
	if c, ok := conn.(pageTargetsContextConn); ok {
		return c.PageTargetsContext(ctx)
	}
	return conn.PageTargets()
}
func createPageTarget(ctx context.Context, conn tabConn, url string, silent bool) (target.ID, error) {
	if c, ok := conn.(createPageTargetContextConn); ok {
		return c.CreatePageTargetContext(ctx, url, silent)
	}
	return conn.CreatePageTarget(url, silent)
}
func closeTarget(ctx context.Context, conn tabConn, id target.ID) error {
	if c, ok := conn.(closeTargetContextConn); ok {
		return c.CloseTargetContext(ctx, id)
	}
	return conn.CloseTarget(id)
}
func navigate(ctx context.Context, conn tabConn, id target.ID, url string) error {
	if c, ok := conn.(navigateContextConn); ok {
		return c.NavigateContext(ctx, id, url)
	}
	return conn.Navigate(id, url)
}
func reload(ctx context.Context, conn tabConn, id target.ID) error {
	if c, ok := conn.(reloadContextConn); ok {
		return c.ReloadContext(ctx, id)
	}
	return conn.Reload(id)
}
func screenshot(ctx context.Context, conn tabConn, id target.ID, format string) ([]byte, string, error) {
	if c, ok := conn.(screenshotContextConn); ok {
		return c.ScreenshotContext(ctx, id, format)
	}
	return conn.Screenshot(id, format)
}
func eval(ctx context.Context, conn tabConn, id target.ID, script string) (json.RawMessage, error) {
	if c, ok := conn.(evalContextConn); ok {
		return c.EvalContext(ctx, id, script)
	}
	return conn.Eval(id, script)
}
func click(ctx context.Context, conn tabConn, id target.ID, selector string) error {
	if c, ok := conn.(clickContextConn); ok {
		return c.ClickContext(ctx, id, selector)
	}
	return conn.Click(id, selector)
}
func fill(ctx context.Context, conn tabConn, id target.ID, selector, text string) error {
	if c, ok := conn.(fillContextConn); ok {
		return c.FillContext(ctx, id, selector, text)
	}
	return conn.Fill(id, selector, text)
}
func fetchPage(ctx context.Context, conn fetchConn, id target.ID, url, method string, headers []byte, body string) (json.RawMessage, error) {
	if c, ok := conn.(fetchContextConn); ok {
		return c.FetchPageContext(ctx, id, url, method, headers, body)
	}
	return conn.FetchPage(id, url, method, headers, body)
}
func snapshot(ctx context.Context, conn snapshotConn, id target.ID, opts browser.SnapshotOpts) (string, string, string, map[string]string, error) {
	if c, ok := conn.(snapshotContextConn); ok {
		return c.SnapshotContext(ctx, id, opts)
	}
	return conn.Snapshot(id, opts)
}
func appendNetworkRoute(ctx context.Context, conn routeConn, id target.ID, rule browser.NetworkRouteRule) error {
	if c, ok := conn.(appendNetworkRouteContextConn); ok {
		return c.AppendNetworkRouteContext(ctx, id, rule)
	}
	return conn.AppendNetworkRoute(id, rule)
}
func removeNetworkRoutes(ctx context.Context, conn routeConn, id target.ID, pattern string) error {
	if c, ok := conn.(removeNetworkRoutesContextConn); ok {
		return c.RemoveNetworkRoutesContext(ctx, id, pattern)
	}
	return conn.RemoveNetworkRoutes(id, pattern)
}

// lockTab serializes CDP operations per short tab id (IMPLEMENTATION_PLAN §8.2).
func (s *Server) lockTab(ctx context.Context, short string) (func(), bool) {
	if short == "" {
		return func() {}, true
	}
	s.tabMuOps.Lock()
	if s.tabCDPLocks == nil {
		s.tabCDPLocks = make(map[string]chan struct{})
	}
	m, ok := s.tabCDPLocks[short]
	if !ok {
		m = make(chan struct{}, 1)
		m <- struct{}{}
		s.tabCDPLocks[short] = m
	}
	s.tabMuOps.Unlock()
	select {
	case <-m:
		return func() { m <- struct{}{} }, true
	case <-ctx.Done():
		return func() {}, false
	}
}
