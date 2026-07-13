package browser

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
)

type recordedObservation struct {
	target target.ID
	seq    uint64
}

type observationRecorder struct {
	seq      atomic.Uint64
	recorded chan recordedObservation
}

func (r *observationRecorder) NextSeq() (uint64, bool) { return r.seq.Add(1), true }
func (r *observationRecorder) RecordNetwork(id target.ID, seq uint64, _ json.RawMessage) {
	r.recorded <- recordedObservation{target: id, seq: seq}
}
func (r *observationRecorder) RecordConsole(id target.ID, seq uint64, _ json.RawMessage) {
	r.recorded <- recordedObservation{target: id, seq: seq}
}
func (r *observationRecorder) RecordError(id target.ID, seq uint64, _ json.RawMessage) {
	r.recorded <- recordedObservation{target: id, seq: seq}
}

func TestObservationListenerDropsWithoutBlocking(t *testing.T) {
	rec := &observationRecorder{recorded: make(chan recordedObservation, 1)}
	obs := &tabObserver{
		tid:   "TAB-A",
		rec:   rec,
		queue: make(chan queuedObservation, 1),
	}
	obs.active.Store(1 << ObservationConsole)
	event := &runtime.EventConsoleAPICalled{Type: runtime.APITypeLog}

	start := time.Now()
	for range 5000 {
		obs.listen(event)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("listener blocked under flood for %s", elapsed)
	}
	if got := obs.lastSeen[ObservationConsole].Load(); got != 5000 {
		t.Fatalf("cursor = %d, want 5000", got)
	}
	if got := obs.dropped[ObservationConsole].Load(); got != 4999 {
		t.Fatalf("dropped = %d, want 4999", got)
	}
	if len(obs.queue) != 1 {
		t.Fatalf("queue len = %d, want 1", len(obs.queue))
	}
}

func TestObservationWorkersKeepTargetsIsolated(t *testing.T) {
	rec := &observationRecorder{recorded: make(chan recordedObservation, 2)}
	newObserver := func(id target.ID) (*tabObserver, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		obs := &tabObserver{tid: id, ctx: ctx, rec: rec, queue: make(chan queuedObservation, 4)}
		obs.active.Store(1 << ObservationConsole)
		go obs.runWriter()
		return obs, cancel
	}
	a, cancelA := newObserver("TAB-A")
	b, cancelB := newObserver("TAB-B")
	defer cancelA()
	defer cancelB()
	event := &runtime.EventConsoleAPICalled{Type: runtime.APITypeLog}
	a.listen(event)
	b.listen(event)

	seen := map[target.ID]uint64{}
	for range 2 {
		select {
		case item := <-rec.recorded:
			seen[item.target] = item.seq
		case <-time.After(time.Second):
			t.Fatal("observation worker timed out")
		}
	}
	if seen["TAB-A"] == 0 || seen["TAB-B"] == 0 || seen["TAB-A"] == seen["TAB-B"] {
		t.Fatalf("isolated records = %#v", seen)
	}
}
