package state

import (
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/chromedp/cdproto/target"
)

// TabRegistry maps CDP target ids to stable short ids for the HTTP API (INV-3).
type TabRegistry struct {
	mu sync.RWMutex

	targetToShort map[target.ID]string
	shortToTarget map[string]target.ID
	selected      string // short id; empty if none
}

func NewTabRegistry() *TabRegistry {
	return &TabRegistry{
		targetToShort: make(map[target.ID]string),
		shortToTarget: make(map[string]target.ID),
	}
}

// SyncPageTargets updates registration from the current set of page-type targets.
// Targets that disappeared are removed (INV-6: release short id and clear association).
func (r *TabRegistry) SyncPageTargets(infos []*target.Info) []TabSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	present := make(map[target.ID]struct{}, len(infos))
	for _, info := range infos {
		if info == nil || !isPageTarget(info) {
			continue
		}
		present[info.TargetID] = struct{}{}
	}

	for id := range r.targetToShort {
		if _, ok := present[id]; !ok {
			r.removeLocked(id)
		}
	}

	ids := make([]target.ID, 0, len(present))
	for id := range present {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b target.ID) int {
		if x, y := string(a), string(b); x < y {
			return -1
		} else if x > y {
			return 1
		}
		return 0
	})
	out := make([]TabSnapshot, 0, len(ids))
	for _, id := range ids {
		short := r.assignShortLocked(id)
		out = append(out, TabSnapshot{
			ShortID:  short,
			TargetID: id,
			Title:    tabTitle(infos, id),
			URL:      tabURL(infos, id),
		})
	}
	return out
}

func tabTitle(infos []*target.Info, id target.ID) string {
	for _, info := range infos {
		if info != nil && info.TargetID == id {
			return info.Title
		}
	}
	return ""
}

func tabURL(infos []*target.Info, id target.ID) string {
	for _, info := range infos {
		if info != nil && info.TargetID == id {
			return info.URL
		}
	}
	return ""
}

// isPageTarget matches top-level browsing targets we expose as tabs.
func isPageTarget(info *target.Info) bool {
	if info == nil {
		return false
	}
	switch info.Type {
	case "page", "tab":
		return true
	default:
		return false
	}
}

// TabSnapshot is one row for API responses after sync.
type TabSnapshot struct {
	ShortID  string
	TargetID target.ID
	Title    string
	URL      string
}

func (r *TabRegistry) removeLocked(id target.ID) {
	short, ok := r.targetToShort[id]
	if !ok {
		return
	}
	delete(r.targetToShort, id)
	delete(r.shortToTarget, short)
	if r.selected == short {
		r.selected = ""
	}
}

// assignShortLocked returns an existing or new short id for target id (caller holds lock).
func (r *TabRegistry) assignShortLocked(id target.ID) string {
	if s, ok := r.targetToShort[id]; ok {
		return s
	}
	hexDigits := hexRunes(string(id))
	if len(hexDigits) == 0 {
		s := sanitizeShort(string(id))
		if oid, taken := r.shortToTarget[s]; taken && oid != id {
			s = string(id)
		}
		r.registerLocked(id, s)
		return s
	}
	for n := 4; n <= len(hexDigits); n++ {
		cand := string(hexDigits[len(hexDigits)-n:])
		if oid, taken := r.shortToTarget[cand]; taken && oid != id {
			continue
		}
		r.registerLocked(id, cand)
		return cand
	}
	// Extremely unlikely: fall back to full hex digit string.
	full := string(hexDigits)
	r.registerLocked(id, full)
	return full
}

func (r *TabRegistry) registerLocked(id target.ID, short string) {
	r.targetToShort[id] = short
	r.shortToTarget[short] = id
}

func hexRunes(s string) []rune {
	var out []rune
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			out = append(out, unicode.ToLower(r))
		case r >= 'a' && r <= 'f':
			out = append(out, r)
		case r >= 'A' && r <= 'F':
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}

func sanitizeShort(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		return s[len(s)-64:]
	}
	return s
}

// Lookup validates short id and returns the CDP target id (INV-3).
func (r *TabRegistry) Lookup(shortID string) (target.ID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	shortID = strings.TrimSpace(shortID)
	if shortID == "" {
		return "", false
	}
	id, ok := r.shortToTarget[shortID]
	return id, ok
}

// Select marks shortID as the focused tab or returns false if unknown.
func (r *TabRegistry) Select(shortID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	shortID = strings.TrimSpace(shortID)
	if shortID == "" {
		return false
	}
	if _, ok := r.shortToTarget[shortID]; !ok {
		return false
	}
	r.selected = shortID
	return true
}

// Selected returns the current focus short id, or "".
func (r *TabRegistry) Selected() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.selected
}
