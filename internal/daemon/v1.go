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
	"github.com/yiplee/go-bb-browser/internal/protocol"
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

	switch method {
	case protocol.MethodTabList:
		s.handleTabList(w, id, params)
	case protocol.MethodTabSelect:
		s.handleTabSelect(w, id, params)
	case protocol.MethodTabNew:
		s.handleTabNew(w, id, params)
	case protocol.MethodGoto:
		s.handleGoto(w, id, params)
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
	s.rpcOK(w, id, protocol.TabListResult{
		Tab:   tabField,
		Seq:   seq,
		Tabs:  items,
		Focus: focus,
	})
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
	if targets, err := conn.PageTargets(); err == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabNewResult{Tab: short, Seq: seq})
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
	if targets, err := conn.PageTargets(); err == nil {
		s.tabs.SyncPageTargets(targets)
	}
	seq := s.seq.Next()
	s.rpcOK(w, id, protocol.TabCloseResult{Tab: tab, Seq: seq})
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
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodClick,
		})
		return
	}
	if sel == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector",
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

func (s *Server) handleFill(w http.ResponseWriter, id json.RawMessage, params json.RawMessage) {
	var p protocol.FillParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "invalid params", nil)
		return
	}
	tab := strings.TrimSpace(p.Tab)
	sel := strings.TrimSpace(p.Selector)
	if tab == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing tab", &protocol.ErrData{
			Error:  "missing tab",
			Method: protocol.MethodFill,
		})
		return
	}
	if sel == "" {
		s.rpcErr(w, id, protocol.CodeInvalidParams, "missing selector", &protocol.ErrData{
			Error:  "missing selector",
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
	return fmt.Sprintf("CDP error (%T)", err)
}

// tabConn is the CDP surface used by /v1 handlers.
type tabConn interface {
	PageTargets() ([]*target.Info, error)
	CreatePageTarget(initialURL string) (target.ID, error)
	CloseTarget(id target.ID) error
	Navigate(tabID target.ID, url string) error
	Screenshot(tabID target.ID, format string) ([]byte, string, error)
	Eval(tabID target.ID, script string) (json.RawMessage, error)
	Click(tabID target.ID, selector string) error
	Fill(tabID target.ID, selector, text string) error
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
