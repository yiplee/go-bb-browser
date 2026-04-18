package daemon

import (
	"encoding/json"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
)

type obsSink struct {
	seq   *state.SeqGen
	store *state.TabObsStore
}

var _ browser.ObsRecorder = (*obsSink)(nil)

func (o *obsSink) RecordNetwork(id target.ID, data json.RawMessage) {
	if o == nil || o.store == nil || o.seq == nil {
		return
	}
	o.store.PushNetwork(id, o.seq.Next(), data)
}

func (o *obsSink) RecordConsole(id target.ID, data json.RawMessage) {
	if o == nil || o.store == nil || o.seq == nil {
		return
	}
	o.store.PushConsole(id, o.seq.Next(), data)
}

func (o *obsSink) RecordError(id target.ID, data json.RawMessage) {
	if o == nil || o.store == nil || o.seq == nil {
		return
	}
	o.store.PushError(id, o.seq.Next(), data)
}
