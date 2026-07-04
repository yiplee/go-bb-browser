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

func applyManagedUpdate(managed map[string]time.Time, rec LogRecord) uint64 {
	if managed == nil || !rec.OK {
		return rec.Seq
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
	return rec.Seq
}

func applyManagedFromLogLine(managed map[string]time.Time, rec LogRecord) {
	applyManagedUpdate(managed, rec)
}
