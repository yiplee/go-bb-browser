package browser

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/state"
)

type targetsExecutor struct {
	calls int
}

func TestDetectForegroundHungTabDoesNotSerializeProbes(t *testing.T) {
	snaps := []state.TabSnapshot{
		{ShortID: "a", TargetID: "A"},
		{ShortID: "b", TargetID: "B"},
		{ShortID: "c", TargetID: "C"},
	}
	var calls atomic.Int64
	start := time.Now()
	got, ok := detectForegroundConcurrent(context.Background(), snaps, 3, 30*time.Millisecond, 200*time.Millisecond,
		func(ctx context.Context, sn state.TabSnapshot) (bool, error) {
			calls.Add(1)
			if sn.TargetID == "A" {
				<-ctx.Done()
				return false, ctx.Err()
			}
			return sn.TargetID == "B", nil
		})
	if ok || got != "" {
		t.Fatalf("got (%q, %v), want incomplete result", got, ok)
	}
	if calls.Load() != 3 {
		t.Fatalf("probe calls = %d, want 3", calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("hung tab serialized probes: %s", elapsed)
	}
}

func (e *targetsExecutor) Execute(_ context.Context, method string, _ any, result any) error {
	e.calls++
	if method != "Target.getTargets" {
		return fmt.Errorf("unexpected method %q", method)
	}
	out, ok := result.(*target.GetTargetsReturns)
	if !ok {
		return fmt.Errorf("unexpected result type %T", result)
	}
	out.TargetInfos = []*target.Info{
		{TargetID: "PAGE", Type: "page"},
		{TargetID: "WORKER", Type: "service_worker"},
	}
	return nil
}

func TestPageTargetsUsesBrowserExecutorDirectly(t *testing.T) {
	ex := &targetsExecutor{}
	// A plain context with only a CDP executor proves this does not require a
	// chromedp target context or attach to any page.
	infos, err := pageTargetsWithExecutor(context.Background(), ex)
	if err != nil {
		t.Fatal(err)
	}
	if ex.calls != 1 {
		t.Fatalf("calls = %d, want 1", ex.calls)
	}
	if len(infos) != 1 || infos[0].TargetID != "PAGE" {
		t.Fatalf("targets = %#v", infos)
	}
}

func TestTargetEventQueueOverflowMarksDirty(t *testing.T) {
	s := &Session{targetEvents: make(chan TargetEvent, 1)}
	s.enqueueTargetEvent(TargetEvent{Destroyed: "A"})
	s.enqueueTargetEvent(TargetEvent{Destroyed: "B"})
	if !s.ConsumeTargetDirty() {
		t.Fatal("queue overflow did not mark target state dirty")
	}
	if s.ConsumeTargetDirty() {
		t.Fatal("dirty flag was not consumed")
	}
}
