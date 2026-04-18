package state

import "encoding/json"

// RingBuffer is a FIFO buffer with a fixed capacity; oldest entries are dropped on overflow.
// Not concurrency-safe; callers must hold an outer lock if needed.
type RingBuffer struct {
	cap     int
	q       []obsEntry
	dropped uint64
}

type obsEntry struct {
	Seq  uint64          `json:"seq"`
	Data json.RawMessage `json:"data"`
}

func newRing(cap int) RingBuffer {
	if cap < 0 {
		cap = 0
	}
	return RingBuffer{cap: cap}
}

// Push appends an entry; if over capacity, discards oldest and increments Dropped.
func (r *RingBuffer) Push(seq uint64, data json.RawMessage) {
	if r.cap <= 0 {
		return
	}
	r.q = append(r.q, obsEntry{Seq: seq, Data: data})
	if len(r.q) > r.cap {
		n := len(r.q) - r.cap
		r.dropped += uint64(n)
		r.q = r.q[n:]
	}
}

// QuerySince returns entries with Seq > since in chronological order, the max seq in the
// buffer (cursor), and cumulative dropped count for this ring.
func (r *RingBuffer) QuerySince(since uint64) (events []obsEntry, cursor uint64, dropped uint64) {
	dropped = r.dropped
	var maxSeq uint64
	for _, e := range r.q {
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
		if e.Seq > since {
			events = append(events, e)
		}
	}
	cursor = maxSeq
	if cursor < since {
		cursor = since
	}
	return events, cursor, dropped
}
