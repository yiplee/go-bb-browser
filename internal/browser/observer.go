package browser

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const observationQueueCapacity = 1024

type ObservationKind uint8

const (
	ObservationNetwork ObservationKind = iota
	ObservationConsole
	ObservationErrors
	observationKindCount
)

type ObservationStats struct {
	Cursor  uint64
	Dropped uint64
}

// ObsRecorder reserves the process-global sequence in the synchronous callback,
// then persists already-sequenced events from a worker goroutine.
type ObsRecorder interface {
	NextSeq() (uint64, bool)
	RecordNetwork(id target.ID, seq uint64, data json.RawMessage)
	RecordConsole(id target.ID, seq uint64, data json.RawMessage)
	RecordError(id target.ID, seq uint64, data json.RawMessage)
}

type queuedObservation struct {
	kind    ObservationKind
	seq     uint64
	payload any
}

type tabObserver struct {
	tid     target.ID
	ctx     context.Context
	cancel  context.CancelFunc
	rec     ObsRecorder
	logger  *slog.Logger
	queue   chan queuedObservation
	ready   chan struct{}
	initErr error

	mu             sync.Mutex
	listenerCancel context.CancelFunc
	active         atomic.Uint32
	lastTouch      [observationKindCount]atomic.Int64
	lastSeen       [observationKindCount]atomic.Uint64
	dropped        [observationKindCount]atomic.Uint64
}

// EnsureObservation activates only the domains needed by kind and renews its lease.
func (s *Session) EnsureObservation(ctx context.Context, tid target.ID, kind ObservationKind, rec ObsRecorder, lg *slog.Logger) error {
	if s == nil || rec == nil {
		return errNilSession()
	}
	if lg == nil {
		lg = slog.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.obsMu.Lock()
	if s.observers == nil {
		s.observers = make(map[target.ID]*tabObserver)
	}
	obs := s.observers[tid]
	if obs == nil {
		tabCtx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(tid))
		obs = &tabObserver{
			tid: tid, ctx: tabCtx, cancel: cancel, rec: rec, logger: lg,
			queue: make(chan queuedObservation, observationQueueCapacity), ready: make(chan struct{}),
		}
		s.observers[tid] = obs
		go obs.runWriter()
		go obs.initialize()
	}
	s.obsMu.Unlock()
	select {
	case <-obs.ready:
		if obs.initErr != nil {
			return obs.initErr
		}
	case <-ctx.Done():
		return wrapCDPError("Target.attachObserver", tid, ctx.Err())
	}
	return obs.activate(ctx, kind)
}

func (o *tabObserver) initialize() {
	// Bind chromedp's Target.run to the observer's long-lived context, never to a
	// request timeout child. Cancelling the latter would otherwise kill the session.
	err := chromedp.Run(o.ctx)
	if err == nil {
		// chromedp enables these domains while attaching. Disable this observer
		// session until a corresponding observation capability is leased.
		initCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = runCDPWithContext(o.ctx, initCtx, network.Disable(), runtime.Disable(), cdplog.Disable())
		cancel()
	}
	o.initErr = wrapCDPError("Target.attachObserver", o.tid, err)
	if o.initErr != nil && o.logger != nil {
		o.logger.Debug("observer target initialization ended", "target", o.tid, "err", o.initErr)
	}
	close(o.ready)
}

func (o *tabObserver) activate(ctx context.Context, kind ObservationKind) error {
	if kind >= observationKindCount {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.listenerCancel == nil {
		listenerCtx, cancel := context.WithCancel(o.ctx)
		o.listenerCancel = cancel
		chromedp.ListenTarget(listenerCtx, o.listen)
	}
	bit := uint32(1) << kind
	wasActive := o.active.Load()
	var actions []chromedp.Action
	switch kind {
	case ObservationNetwork:
		if wasActive&bit == 0 {
			actions = append(actions, network.Enable())
		}
	case ObservationConsole:
		if wasActive&(1<<ObservationConsole|1<<ObservationErrors) == 0 {
			actions = append(actions, runtime.Enable())
		}
	case ObservationErrors:
		if wasActive&(1<<ObservationConsole|1<<ObservationErrors) == 0 {
			actions = append(actions, runtime.Enable())
		}
		if wasActive&bit == 0 {
			actions = append(actions, cdplog.Enable())
		}
	}
	if len(actions) > 0 {
		if err := runCDPWithContext(o.ctx, ctx, actions...); err != nil {
			return err
		}
	}
	o.active.Store(wasActive | bit)
	o.lastTouch[kind].Store(time.Now().UnixNano())
	return nil
}

func (o *tabObserver) listen(ev any) {
	active := o.active.Load()
	var kind ObservationKind
	var payload any
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		if active&(1<<ObservationNetwork) == 0 || e == nil || e.Request == nil {
			return
		}
		kind = ObservationNetwork
		payload = map[string]any{"kind": "request", "requestId": string(e.RequestID), "url": e.Request.URL, "method": e.Request.Method}
	case *network.EventResponseReceived:
		if active&(1<<ObservationNetwork) == 0 || e == nil || e.Response == nil {
			return
		}
		kind = ObservationNetwork
		payload = map[string]any{"kind": "response", "requestId": string(e.RequestID), "status": e.Response.Status, "mimeType": e.Response.MimeType, "url": e.Response.URL}
	case *network.EventLoadingFailed:
		if active&(1<<ObservationNetwork) == 0 || e == nil {
			return
		}
		kind = ObservationNetwork
		payload = map[string]any{"kind": "loadingFailed", "requestId": string(e.RequestID), "errorText": e.ErrorText, "canceled": e.Canceled}
	case *runtime.EventConsoleAPICalled:
		if active&(1<<ObservationConsole) == 0 || e == nil {
			return
		}
		kind = ObservationConsole
		payload = map[string]any{"type": string(e.Type), "args": e.Args}
	case *runtime.EventExceptionThrown:
		if active&(1<<ObservationErrors) == 0 || e == nil || e.ExceptionDetails == nil {
			return
		}
		kind = ObservationErrors
		d := e.ExceptionDetails
		payload = map[string]any{"text": d.Text, "lineNumber": d.LineNumber, "columnNumber": d.ColumnNumber, "exception": d.Exception}
	case *cdplog.EventEntryAdded:
		if active&(1<<ObservationErrors) == 0 || e == nil || e.Entry == nil {
			return
		}
		kind = ObservationErrors
		ent := e.Entry
		payload = map[string]any{"source": string(ent.Source), "level": string(ent.Level), "text": ent.Text, "url": ent.URL, "lineNumber": ent.LineNumber}
	default:
		return
	}
	seq, ok := o.rec.NextSeq()
	if !ok {
		return
	}
	storeAtomicMax(&o.lastSeen[kind], seq)
	item := queuedObservation{kind: kind, seq: seq, payload: payload}
	select {
	case o.queue <- item:
	default:
		o.dropped[kind].Add(1)
	}
}

func storeAtomicMax(dst *atomic.Uint64, value uint64) {
	for current := dst.Load(); value > current; current = dst.Load() {
		if dst.CompareAndSwap(current, value) {
			return
		}
	}
}

func (o *tabObserver) runWriter() {
	for {
		select {
		case <-o.ctx.Done():
			return
		case item := <-o.queue:
			data, err := json.Marshal(item.payload)
			if err != nil {
				o.dropped[item.kind].Add(1)
				continue
			}
			switch item.kind {
			case ObservationNetwork:
				o.rec.RecordNetwork(o.tid, item.seq, data)
			case ObservationConsole:
				o.rec.RecordConsole(o.tid, item.seq, data)
			case ObservationErrors:
				o.rec.RecordError(o.tid, item.seq, data)
			}
		}
	}
}

func (s *Session) ObservationStats(tid target.ID, kind ObservationKind) ObservationStats {
	s.obsMu.Lock()
	obs := s.observers[tid]
	s.obsMu.Unlock()
	if obs == nil || kind >= observationKindCount {
		return ObservationStats{}
	}
	return ObservationStats{Cursor: obs.lastSeen[kind].Load(), Dropped: obs.dropped[kind].Load()}
}

// SweepObservers expires capabilities without cancelling the chromedp target
// context. Cancelling a live child context would close the user's Chrome tab.
func (s *Session) SweepObservers(now time.Time, idle time.Duration) {
	if s == nil || idle <= 0 {
		return
	}
	s.obsMu.Lock()
	list := make([]*tabObserver, 0, len(s.observers))
	for _, obs := range s.observers {
		list = append(list, obs)
	}
	s.obsMu.Unlock()
	for _, obs := range list {
		obs.expire(now, idle)
	}
}

func (o *tabObserver) expire(now time.Time, idle time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	active := o.active.Load()
	remaining := active
	for kind := ObservationKind(0); kind < observationKindCount; kind++ {
		bit := uint32(1) << kind
		if active&bit == 0 {
			continue
		}
		last := time.Unix(0, o.lastTouch[kind].Load())
		if now.Sub(last) >= idle {
			remaining &^= bit
		}
	}
	if remaining == active {
		return
	}
	var actions []chromedp.Action
	if active&(1<<ObservationNetwork) != 0 && remaining&(1<<ObservationNetwork) == 0 {
		actions = append(actions, network.Disable())
	}
	if active&(1<<ObservationErrors) != 0 && remaining&(1<<ObservationErrors) == 0 {
		actions = append(actions, cdplog.Disable())
	}
	if active&(1<<ObservationConsole|1<<ObservationErrors) != 0 && remaining&(1<<ObservationConsole|1<<ObservationErrors) == 0 {
		actions = append(actions, runtime.Disable())
	}
	if len(actions) > 0 {
		ctx, cancel := context.WithTimeout(o.ctx, 2*time.Second)
		_ = chromedp.Run(ctx, actions...)
		cancel()
	}
	o.active.Store(remaining)
	if remaining == 0 && o.listenerCancel != nil {
		o.listenerCancel()
		o.listenerCancel = nil
	}
}
