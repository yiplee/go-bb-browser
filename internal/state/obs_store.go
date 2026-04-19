package state

import (
	"encoding/json"
	"sync"

	"github.com/chromedp/cdproto/target"
)

// Default observation ring capacities (IMPLEMENTATION_PLAN §6.2).
const (
	DefaultNetworkCap = 500
	DefaultConsoleCap = 200
	DefaultErrorsCap  = 100
)

// TabObsStore holds per-target network / console / error observation rings (INV-5).
type TabObsStore struct {
	mu sync.Mutex

	netCap  int
	consCap int
	errCap  int

	targets map[target.ID]*targetObsRings
}

type targetObsRings struct {
	net  RingBuffer
	cons RingBuffer
	err  RingBuffer
}

func NewTabObsStore() *TabObsStore {
	return NewTabObsStoreWithCaps(DefaultNetworkCap, DefaultConsoleCap, DefaultErrorsCap)
}

func NewTabObsStoreWithCaps(netCap, consCap, errCap int) *TabObsStore {
	return &TabObsStore{
		netCap:  netCap,
		consCap: consCap,
		errCap:  errCap,
		targets: make(map[target.ID]*targetObsRings),
	}
}

func (s *TabObsStore) getOrCreateLocked(id target.ID) *targetObsRings {
	tb := s.targets[id]
	if tb == nil {
		tb = &targetObsRings{
			net:  newRing(s.netCap),
			cons: newRing(s.consCap),
			err:  newRing(s.errCap),
		}
		s.targets[id] = tb
	}
	return tb
}

// SyncPresence removes observation state for targets that are no longer present (INV-6).
func (s *TabObsStore) SyncPresence(infos []*target.Info) {
	present := make(map[target.ID]struct{})
	for _, info := range infos {
		if info == nil {
			continue
		}
		switch info.Type {
		case "page", "tab":
			present[info.TargetID] = struct{}{}
		default:
			continue
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.targets {
		if _, ok := present[id]; !ok {
			delete(s.targets, id)
		}
	}
}

// ClearTarget removes rings for one target (best-effort; INV-6).
func (s *TabObsStore) ClearTarget(id target.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.targets, id)
}

// ClearNetworkOnly clears only the network ring for a target (keep console/errors).
func (s *TabObsStore) ClearNetworkOnly(id target.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return
	}
	tb.net = newRing(s.netCap)
}

// ClearConsoleOnly clears only the console ring for a target.
func (s *TabObsStore) ClearConsoleOnly(id target.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return
	}
	tb.cons = newRing(s.consCap)
}

// ClearErrorsOnly clears only the errors ring for a target.
func (s *TabObsStore) ClearErrorsOnly(id target.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return
	}
	tb.err = newRing(s.errCap)
}

// PushNetwork records a network observation with the given global seq.
func (s *TabObsStore) PushNetwork(id target.ID, seq uint64, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.getOrCreateLocked(id)
	tb.net.Push(seq, data)
}

// PushConsole records a console observation.
func (s *TabObsStore) PushConsole(id target.ID, seq uint64, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.getOrCreateLocked(id)
	tb.cons.Push(seq, data)
}

// PushError records an error / exception observation.
func (s *TabObsStore) PushError(id target.ID, seq uint64, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.getOrCreateLocked(id)
	tb.err.Push(seq, data)
}

// QueryNetwork returns events with seq > since for the given CDP target id.
func (s *TabObsStore) QueryNetwork(id target.ID, since uint64) (events []ObsEvent, cursor uint64, dropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return nil, since, 0
	}
	raw, cursor, dropped := tb.net.QuerySince(since)
	return entriesToObs(raw), cursor, dropped
}

// QueryConsole returns console events with seq > since.
func (s *TabObsStore) QueryConsole(id target.ID, since uint64) (events []ObsEvent, cursor uint64, dropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return nil, since, 0
	}
	raw, cursor, dropped := tb.cons.QuerySince(since)
	return entriesToObs(raw), cursor, dropped
}

// QueryErrors returns error events with seq > since.
func (s *TabObsStore) QueryErrors(id target.ID, since uint64) (events []ObsEvent, cursor uint64, dropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tb := s.targets[id]
	if tb == nil {
		return nil, since, 0
	}
	raw, cursor, dropped := tb.err.QuerySince(since)
	return entriesToObs(raw), cursor, dropped
}

// ObsEvent is one buffered observation for JSON APIs.
type ObsEvent struct {
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

func entriesToObs(in []obsEntry) []ObsEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]ObsEvent, 0, len(in))
	for _, e := range in {
		out = append(out, ObsEvent{Seq: e.Seq, Data: e.Data})
	}
	return out
}
