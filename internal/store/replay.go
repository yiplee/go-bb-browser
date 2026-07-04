package store

import (
	"time"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func cloneManaged(in map[string]time.Time) map[string]time.Time {
	if len(in) == 0 {
		return make(map[string]time.Time)
	}
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// applyManagedUpdate folds one log record into the managed-tab activity map:
// tab_new registers a tab, tab_close releases it, and other tab-related methods
// refresh its last-activity time.
func applyManagedUpdate(managed map[string]time.Time, rec LogRecord) {
	if managed == nil || !rec.OK {
		return
	}
	tab := rec.Tab
	if tab == "" {
		tab = tabFromRequestBody(rec.Body)
	}
	switch rec.Action {
	case protocol.MethodTabNew:
		if tab != "" {
			managed[tab] = rec.Time
		}
	case protocol.MethodTabClose:
		if tab != "" {
			delete(managed, tab)
		}
	default:
		if tab != "" && protocol.IsTabRelatedMethod(rec.Action) && rec.Action != protocol.MethodTabList {
			if _, isManaged := managed[tab]; isManaged {
				managed[tab] = rec.Time
			}
		}
	}
}
