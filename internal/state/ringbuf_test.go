package state

import (
	"encoding/json"
	"testing"
)

func TestRingBufferDropAndSince(t *testing.T) {
	r := newRing(3)
	r.Push(1, json.RawMessage(`"a"`))
	r.Push(2, json.RawMessage(`"b"`))
	r.Push(3, json.RawMessage(`"c"`))
	r.Push(4, json.RawMessage(`"d"`))

	ev, cursor, dropped := r.QuerySince(0)
	if dropped != 1 {
		t.Fatalf("dropped %d want 1", dropped)
	}
	if len(ev) != 3 {
		t.Fatalf("len %d want 3", len(ev))
	}
	if ev[0].Seq != 2 || string(ev[0].Data) != `"b"` {
		t.Fatalf("first event %#v", ev[0])
	}
	if cursor != 4 {
		t.Fatalf("cursor %d want 4", cursor)
	}

	ev2, _, _ := r.QuerySince(3)
	if len(ev2) != 1 || ev2[0].Seq != 4 {
		t.Fatalf("since filter %#v", ev2)
	}
}
