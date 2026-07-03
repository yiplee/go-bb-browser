package daemon

import (
	"encoding/json"
	"log/slog"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/internal/store"
)

type obsSink struct {
	store  *store.Store
	obs    *state.TabObsStore
	logger *slog.Logger
}

var _ browser.ObsRecorder = (*obsSink)(nil)

func (o *obsSink) nextSeq() (uint64, bool) {
	if o == nil || o.store == nil {
		return 0, false
	}
	n, err := o.store.NextSeq()
	if err != nil {
		if o.logger != nil {
			o.logger.Warn("observation seq failed", "err", err)
		}
		return 0, false
	}
	return n, true
}

func (o *obsSink) RecordNetwork(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	seq, ok := o.nextSeq()
	if !ok {
		return
	}
	o.obs.PushNetwork(id, seq, data)
}

func (o *obsSink) RecordConsole(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	seq, ok := o.nextSeq()
	if !ok {
		return
	}
	o.obs.PushConsole(id, seq, data)
}

func (o *obsSink) RecordError(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	seq, ok := o.nextSeq()
	if !ok {
		return
	}
	o.obs.PushError(id, seq, data)
}
