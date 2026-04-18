package protocol

import "encoding/json"

// JSON-RPC 2.0 method names (same string values as legacy "action" field).
const (
	MethodTabList        = "tab_list"
	MethodTabFocus       = "tab_focus"
	MethodTabSelect      = "tab_select"
	MethodTabNew         = "tab_new"
	MethodGoto           = "goto"
	MethodReload         = "reload"
	MethodTabClose       = "tab_close"
	MethodScreenshot     = "screenshot"
	MethodEval           = "eval"
	MethodClick          = "click"
	MethodFill           = "fill"
	MethodNetwork        = "network"
	MethodNetworkClear   = "network_clear"
	MethodNetworkRoute   = "network_route"
	MethodNetworkUnroute = "network_unroute"
	MethodFetch          = "fetch"
	MethodSnapshot       = "snapshot"
	MethodConsole        = "console"
	MethodConsoleClear   = "console_clear"
	MethodErrors         = "errors"
	MethodErrorsClear    = "errors_clear"
)

// Legacy aliases — same values as Method*.
const (
	ActionTabList   = MethodTabList
	ActionTabFocus  = MethodTabFocus
	ActionTabSelect = MethodTabSelect
	ActionTabNew    = MethodTabNew
	ActionGoto      = MethodGoto
	ActionReload    = MethodReload
	ActionTabClose  = MethodTabClose
)

// JSON-RPC 2.0 error codes (subset).
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeServerError    = -32000
)

// Request is a single JSON-RPC 2.0 request object for POST /v1.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

// Response is a JSON-RPC 2.0 response object.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// ResponseError is the JSON-RPC error object.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ErrData carries daemon UX fields inside error.data (optional).
type ErrData struct {
	Error  string `json:"error"`
	Hint   string `json:"hint,omitempty"`
	Method string `json:"method,omitempty"`
}

// --- Params per method ---

// TabListParams is optional for tab_list (empty object).
type TabListParams struct{}

// TabFocusParams is optional for tab_focus (empty object).
type TabFocusParams struct{}

type TabSelectParams struct {
	Tab string `json:"tab"`
}

type TabNewParams struct {
	URL string `json:"url,omitempty"`
}

type GotoParams struct {
	Tab string `json:"tab"`
	URL string `json:"url"`
}

type ReloadParams struct {
	Tab string `json:"tab"`
}

type TabCloseParams struct {
	Tab string `json:"tab"`
}

type ScreenshotParams struct {
	Tab    string `json:"tab"`
	Format string `json:"format,omitempty"` // "png" (default) or "jpeg"
}

type EvalParams struct {
	Tab    string `json:"tab"`
	Script string `json:"script"`
}

type ClickParams struct {
	Tab      string `json:"tab"`
	Selector string `json:"selector"`
	Ref      string `json:"ref,omitempty"` // numeric id from snapshot (@1 → "1"); expands to __bb_snap_ref selector
}

type FillParams struct {
	Tab      string `json:"tab"`
	Selector string `json:"selector"`
	Ref      string `json:"ref,omitempty"`
	Text     string `json:"text"`
}

// SnapshotParams builds a compact page snapshot with @ref → CSS selector mapping.
type SnapshotParams struct {
	Tab             string `json:"tab"`
	InteractiveOnly bool   `json:"interactive_only,omitempty"`
	PruneEmpty      bool   `json:"prune_empty,omitempty"`
	MaxDepth        int    `json:"max_depth,omitempty"` // 0 = unlimited
	SelectorScope   string `json:"selector_scope,omitempty"`
}

// FetchParams runs fetch() in the page context (credentials included).
type FetchParams struct {
	Tab     string `json:"tab"`
	URL     string `json:"url"`
	Method  string `json:"method,omitempty"`
	Headers string `json:"headers,omitempty"` // JSON object string
	Body    string `json:"body,omitempty"`
}

// NetworkRouteParams registers a Fetch interception rule for the tab.
type NetworkRouteParams struct {
	Tab         string `json:"tab"`
	URLPattern  string `json:"url_pattern"`
	Abort       bool   `json:"abort,omitempty"`
	Body        string `json:"body,omitempty"` // mock response body (UTF-8); JSON object/array recommended
	ContentType string `json:"content_type,omitempty"`
	Status      int    `json:"status,omitempty"` // mock HTTP status (default 200)
}

// NetworkUnrouteParams removes interception rules.
type NetworkUnrouteParams struct {
	Tab        string `json:"tab"`
	URLPattern string `json:"url_pattern,omitempty"` // empty = remove all rules for tab
}

// NetworkClearParams clears buffered network observations for a tab (does not affect routes).
type NetworkClearParams struct {
	Tab string `json:"tab"`
}

// ConsoleClearParams clears buffered console messages for a tab.
type ConsoleClearParams struct {
	Tab string `json:"tab"`
}

// ErrorsClearParams clears buffered JS errors / log entries for a tab.
type ErrorsClearParams struct {
	Tab string `json:"tab"`
}

// ObsQueryParams is shared by network / console / errors (INV-2 since filter).
type ObsQueryParams struct {
	Tab   string  `json:"tab"`
	Since *uint64 `json:"since,omitempty"`
}

// TabListItem is one page target after sync with the browser.
type TabListItem struct {
	Tab   string `json:"tab"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// --- Result payloads (nested under "result") — INV-1 tab + seq where applicable ---

type TabListResult struct {
	Tab   string        `json:"tab,omitempty"`
	Seq   uint64        `json:"seq"`
	Tabs  []TabListItem `json:"tabs"`
	Focus string        `json:"focus,omitempty"`
}

// TabFocusResult is the focused / operational tab after sync (same `tab` resolution as tab_list).
type TabFocusResult struct {
	Tab   string `json:"tab"`
	Focus string `json:"focus,omitempty"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Seq   uint64 `json:"seq"`
}

type TabSelectResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type TabNewResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type GotoResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type ReloadResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type TabCloseResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type ScreenshotResult struct {
	Tab  string `json:"tab"`
	Seq  uint64 `json:"seq"`
	Data string `json:"data"` // base64
	MIME string `json:"mime,omitempty"`
}

type EvalResult struct {
	Tab    string          `json:"tab"`
	Seq    uint64          `json:"seq"`
	Result json.RawMessage `json:"result"`
}

type ClickResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type FillResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type SnapshotResult struct {
	Tab   string            `json:"tab"`
	Seq   uint64            `json:"seq"`
	Title string            `json:"title"`
	URL   string            `json:"url"`
	Text  string            `json:"text"`
	Refs  map[string]string `json:"refs"`
}

type FetchResult struct {
	Tab    string          `json:"tab"`
	Seq    uint64          `json:"seq"`
	OK     bool            `json:"ok"`
	Status int             `json:"status"`
	Result json.RawMessage `json:"result"` // parsed JSON value from script (status, headers, body, …)
}

type NetworkRouteResult struct {
	Tab    string `json:"tab"`
	Seq    uint64 `json:"seq"`
	Routes int    `json:"routes"`
}

type NetworkUnrouteResult struct {
	Tab    string `json:"tab"`
	Seq    uint64 `json:"seq"`
	Routes int    `json:"routes"`
}

type NetworkClearResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type ConsoleClearResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

type ErrorsClearResult struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

// ObsEvent is one buffered observation (seq-tagged, INV-4).
type ObsEvent struct {
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

// ObsQueryResult is the payload for network / console / errors queries (INV-1, INV-2).
type ObsQueryResult struct {
	Tab     string     `json:"tab"`
	Seq     uint64     `json:"seq"`
	Cursor  uint64     `json:"cursor"`
	Events  []ObsEvent `json:"events"`
	Dropped uint64     `json:"dropped,omitempty"`
}

// NormalizeParams returns JSON object bytes for unmarshaling; accepts null / missing.
func NormalizeParams(p json.RawMessage) json.RawMessage {
	if len(p) == 0 {
		return []byte("{}")
	}
	if string(p) == "null" {
		return []byte("{}")
	}
	return p
}

// RequestHasID reports whether id is present (JSON-RPC notifications are unsupported).
func RequestHasID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	if string(id) == "null" {
		return false
	}
	return true
}

// MarshalResponse builds a full JSON-RPC response with result.
func MarshalResponse(id json.RawMessage, result any) ([]byte, error) {
	res, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Response{
		JSONRPC: "2.0",
		Result:  res,
		ID:      id,
	})
}

// MarshalErrorResponse builds a JSON-RPC response with error.
func MarshalErrorResponse(id json.RawMessage, code int, message string, data *ErrData) ([]byte, error) {
	var errObj ResponseError
	errObj.Code = code
	errObj.Message = message
	if data != nil {
		d, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		errObj.Data = d
	}
	return json.Marshal(Response{
		JSONRPC: "2.0",
		Error:   &errObj,
		ID:      id,
	})
}

// NullID is JSON null for responses when the request id is unknown.
var NullID = json.RawMessage(`null`)
