package protocol

import "encoding/json"

// Well-known action names for POST /v1.
const (
	ActionTabList   = "tab_list"
	ActionTabSelect = "tab_select"
	ActionTabNew    = "tab_new"
	ActionGoto      = "goto"
	ActionTabClose  = "tab_close"
)

// V1Request is the JSON envelope for daemon commands.
// Tab is required for goto, tab_close, tab_select; tab_list does not use tab.
// URL is used by goto (required) and optionally by tab_new (initial navigation URL).
type V1Request struct {
	Action string `json:"action"`
	Tab    string `json:"tab,omitempty"`
	URL    string `json:"url,omitempty"`
}

// V1Error is returned on failure with HTTP 4xx/5xx and JSON body.
type V1Error struct {
	Error  string `json:"error"`
	Hint   string `json:"hint,omitempty"`
	Action string `json:"action,omitempty"`
}

// TabListItem is one page target after sync with the browser.
type TabListItem struct {
	Tab   string `json:"tab"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// TabListOK is the success body for tab_list (INV-1).
// Tab is the focused short id if still present, else the first tab in sorted order; omitted when there are zero tabs.
type TabListOK struct {
	Tab   string        `json:"tab,omitempty"`
	Seq   uint64        `json:"seq"`
	Tabs  []TabListItem `json:"tabs"`
	Focus string        `json:"focus,omitempty"`
}

// TabSelectOK is the success body for tab_select (INV-1).
type TabSelectOK struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

// TabNewOK is the success body for tab_new (INV-1).
type TabNewOK struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

// GotoOK is the success body for goto (INV-1).
type GotoOK struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

// TabCloseOK is the success body for tab_close (INV-1).
type TabCloseOK struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

func MarshalTabList(ok TabListOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalTabSelect(ok TabSelectOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalTabNew(ok TabNewOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalGoto(ok GotoOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalTabClose(ok TabCloseOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalError(e V1Error) ([]byte, error) {
	return json.Marshal(e)
}
