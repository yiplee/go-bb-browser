package browser

import (
	"context"
	"encoding/json"
	"log/slog"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// ObsRecorder receives CDP observation events keyed by page target id. Implementations
// should assign global monotonic seq and persist (e.g. ring buffers) — see daemon.
type ObsRecorder interface {
	RecordNetwork(id target.ID, data json.RawMessage)
	RecordConsole(id target.ID, data json.RawMessage)
	RecordError(id target.ID, data json.RawMessage)
}

// SyncObservers starts listeners for new page targets and stops observers for targets
// that disappeared. parent should be cancelled on daemon shutdown.
func (s *Session) SyncObservers(parent context.Context, infos []*target.Info, rec ObsRecorder, lg *slog.Logger) {
	if s == nil || rec == nil {
		return
	}
	if lg == nil {
		lg = slog.Default()
	}

	s.obsMu.Lock()
	defer s.obsMu.Unlock()

	if s.observers == nil {
		s.observers = make(map[target.ID]context.CancelFunc)
	}

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

	for id, cancel := range s.observers {
		if _, ok := present[id]; !ok {
			cancel()
			delete(s.observers, id)
		}
	}

	for id := range present {
		if _, ok := s.observers[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(parent)
		s.observers[id] = cancel
		go s.runTabObserver(ctx, id, rec, lg)
	}
}

func (s *Session) runTabObserver(parent context.Context, tid target.ID, rec ObsRecorder, lg *slog.Logger) {
	tabCtx, tabCancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(tid))
	defer tabCancel()

	go func() {
		select {
		case <-parent.Done():
			tabCancel()
		case <-tabCtx.Done():
		}
	}()

	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if e == nil || e.Request == nil {
				return
			}
			b, err := json.Marshal(map[string]any{
				"kind":      "request",
				"requestId": string(e.RequestID),
				"url":       e.Request.URL,
				"method":    e.Request.Method,
			})
			if err != nil {
				return
			}
			rec.RecordNetwork(tid, b)
		case *network.EventResponseReceived:
			if e == nil || e.Response == nil {
				return
			}
			b, err := json.Marshal(map[string]any{
				"kind":      "response",
				"requestId": string(e.RequestID),
				"status":    e.Response.Status,
				"mimeType":  e.Response.MimeType,
				"url":       e.Response.URL,
			})
			if err != nil {
				return
			}
			rec.RecordNetwork(tid, b)
		case *network.EventLoadingFailed:
			if e == nil {
				return
			}
			b, err := json.Marshal(map[string]any{
				"kind":      "loadingFailed",
				"requestId": string(e.RequestID),
				"errorText": e.ErrorText,
				"canceled":  e.Canceled,
			})
			if err != nil {
				return
			}
			rec.RecordNetwork(tid, b)
		case *runtime.EventConsoleAPICalled:
			if e == nil {
				return
			}
			b, err := json.Marshal(map[string]any{
				"type": string(e.Type),
				"args": e.Args,
			})
			if err != nil {
				return
			}
			rec.RecordConsole(tid, b)
		case *runtime.EventExceptionThrown:
			if e == nil || e.ExceptionDetails == nil {
				return
			}
			d := e.ExceptionDetails
			b, err := json.Marshal(map[string]any{
				"text":         d.Text,
				"lineNumber":   d.LineNumber,
				"columnNumber": d.ColumnNumber,
				"exception":    d.Exception,
			})
			if err != nil {
				return
			}
			rec.RecordError(tid, b)
		case *cdplog.EventEntryAdded:
			if e == nil {
				return
			}
			ent := e.Entry
			b, err := json.Marshal(map[string]any{
				"source":     string(ent.Source),
				"level":      string(ent.Level),
				"text":       ent.Text,
				"url":        ent.URL,
				"lineNumber": ent.LineNumber,
			})
			if err != nil {
				return
			}
			rec.RecordError(tid, b)
		}
	})

	if err := chromedp.Run(tabCtx,
		network.Enable(),
		runtime.Enable(),
		cdplog.Enable(),
	); err != nil && tabCtx.Err() == nil {
		lg.Debug("tab observer enable ended", "target", tid, "err", err)
	}
	<-tabCtx.Done()
}
