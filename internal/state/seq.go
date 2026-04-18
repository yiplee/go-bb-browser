package state

import "sync"

// SeqGen produces a globally monotonic sequence number (INV-4).
type SeqGen struct {
	mu  sync.Mutex
	val uint64
}

// Next returns the next seq value (strictly increasing).
func (s *SeqGen) Next() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.val++
	return s.val
}

// Current returns the last issued seq, or 0 if none.
func (s *SeqGen) Current() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.val
}
