package daemon

import (
	"encoding/json"

	"github.com/chromedp/cdproto/target"
	"github.com/yiplee/go-bb-browser/internal/browser"
	"github.com/yiplee/go-bb-browser/internal/state"
	"github.com/yiplee/go-bb-browser/internal/store"
)

type obsSink struct {
	store *store.Store
	obs   *state.TabObsStore
}

var _ browser.ObsRecorder = (*obsSink)(nil)

func (o *obsSink) nextSeq() uint64 {
	if o == nil || o.store == nil {
		return 0
	}
	n, err := o.store.NextSeq()
	if err != nil {
		return 0
	}
	return n
}

func (o *obsSink) RecordNetwork(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	o.obs.PushNetwork(id, o.nextSeq(), data)
}

func (o *obsSink) RecordConsole(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	o.obs.PushConsole(id, o.nextSeq(), data)
}

func (o *obsSink) RecordError(id target.ID, data json.RawMessage) {
	if o == nil || o.obs == nil {
		return
	}
	o.obs.PushError(id, o.nextSeq(), data)
}
