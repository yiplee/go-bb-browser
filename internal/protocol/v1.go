package protocol

import "encoding/json"

// Well-known action names for POST /v1 (Phase 1 subset).
const (
	ActionTabList   = "tab_list"
	ActionTabSelect = "tab_select"
)

// V1Request is the JSON envelope for daemon commands.
type V1Request struct {
	Action string `json:"action"`
	// Tab is the short tab id where the action applies (e.g. tab_select).
	Tab string `json:"tab,omitempty"`
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
type TabListOK struct {
	Tab   string        `json:"tab"`
	Seq   uint64        `json:"seq"`
	Tabs  []TabListItem `json:"tabs"`
	Focus string        `json:"focus,omitempty"` // selected short id, if any
}

// TabSelectOK is the success body for tab_select (INV-1).
type TabSelectOK struct {
	Tab string `json:"tab"`
	Seq uint64 `json:"seq"`
}

// MarshalJSONV1 writes a success object with optional top-level fields.
func MarshalTabList(ok TabListOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalTabSelect(ok TabSelectOK) ([]byte, error) {
	return json.Marshal(ok)
}

func MarshalError(e V1Error) ([]byte, error) {
	return json.Marshal(e)
}
