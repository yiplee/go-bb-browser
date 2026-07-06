package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// foregroundProbeTimeout bounds per-tab visibility probing during tab_list focus sync.
const foregroundProbeTimeout = 3 * time.Second

type probeSessionKey struct{}

func withProbeSession(ctx context.Context, sessionID target.SessionID) context.Context {
	return context.WithValue(ctx, probeSessionKey{}, sessionID)
}

func probeSessionFrom(ctx context.Context) target.SessionID {
	if v := ctx.Value(probeSessionKey{}); v != nil {
		if sid, ok := v.(target.SessionID); ok {
			return sid
		}
	}
	return ""
}

// wsProbe is a minimal CDP client on a dedicated browser websocket used for
// ephemeral attach/eval/detach without chromedp.NewContext (which enables all
// domains and can hang on chrome:// pages).
type wsProbe struct {
	conn *chromedp.Conn

	seq int64
	mu  sync.Mutex
	wait map[int64]chan *cdproto.Message

	readDone chan struct{}
	readErr  error
}

func browserWebSocketURL(ctx context.Context, debuggerBase string) (string, error) {
	base := strings.TrimSpace(debuggerBase)
	if base == "" {
		return "", fmt.Errorf("debugger base URL is empty")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(base, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GET /json/version: HTTP %d", resp.StatusCode)
	}
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("webSocketDebuggerUrl missing in /json/version")
	}
	return v.WebSocketDebuggerURL, nil
}

func dialWSProbeURL(parent context.Context, wsURL string) (*wsProbe, error) {
	conn, err := chromedp.DialContext(parent, wsURL)
	if err != nil {
		return nil, err
	}
	p := &wsProbe{
		conn:     conn,
		wait:     make(map[int64]chan *cdproto.Message),
		readDone: make(chan struct{}),
	}
	go p.readLoop()
	return p, nil
}

func (p *wsProbe) readLoop() {
	defer close(p.readDone)
	for {
		msg := new(cdproto.Message)
		if err := p.conn.Read(context.Background(), msg); err != nil {
			p.readErr = err
			p.failAll(err)
			return
		}
		if msg.ID == 0 {
			continue
		}
		p.mu.Lock()
		ch := p.wait[msg.ID]
		delete(p.wait, msg.ID)
		p.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (p *wsProbe) failAll(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.wait {
		delete(p.wait, id)
		select {
		case ch <- nil:
		default:
		}
		_ = id
	}
	_ = err
}

func (p *wsProbe) close() {
	_ = p.conn.Close()
	<-p.readDone
}

func (p *wsProbe) Execute(ctx context.Context, method string, params, res any) error {
	id := atomic.AddInt64(&p.seq, 1)
	ch := make(chan *cdproto.Message, 1)
	p.mu.Lock()
	p.wait[id] = ch
	p.mu.Unlock()

	var buf []byte
	if params != nil {
		var err error
		if buf, err = json.Marshal(params); err != nil {
			p.mu.Lock()
			delete(p.wait, id)
			p.mu.Unlock()
			return err
		}
	}
	msg := &cdproto.Message{
		ID:        id,
		SessionID: probeSessionFrom(ctx),
		Method:    cdproto.MethodType(method),
		Params:    buf,
	}
	if err := p.conn.Write(ctx, msg); err != nil {
		p.mu.Lock()
		delete(p.wait, id)
		p.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-ch:
		if resp == nil {
			if p.readErr != nil {
				return p.readErr
			}
			return fmt.Errorf("cdp probe connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if res != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, res); err != nil {
				return err
			}
		}
		return nil
	}
}

func visibilityFromRemoteObject(ro *runtime.RemoteObject) (string, error) {
	if ro == nil || len(ro.Value) == 0 {
		return "", fmt.Errorf("empty visibility result")
	}
	var vis string
	if err := json.Unmarshal(ro.Value, &vis); err != nil {
		return "", err
	}
	return vis, nil
}

func (p *wsProbe) tabVisibility(parentCtx context.Context, tabID target.ID) (string, error) {
	probeCtx, cancel := context.WithTimeout(parentCtx, foregroundProbeTimeout)
	defer cancel()

	browserCtx := cdp.WithExecutor(probeCtx, p)
	sessionID, err := target.AttachToTarget(tabID).WithFlatten(true).Do(browserCtx)
	if err != nil {
		return "", err
	}
	defer func() {
		detachCtx, detachCancel := context.WithTimeout(context.Background(), time.Second)
		defer detachCancel()
		_ = target.DetachFromTarget().WithSessionID(sessionID).Do(cdp.WithExecutor(detachCtx, p))
	}()

	sessCtx := withProbeSession(cdp.WithExecutor(probeCtx, p), sessionID)
	if err := runtime.Enable().Do(sessCtx); err != nil {
		return "", err
	}
	ro, exc, err := runtime.Evaluate(`document.visibilityState`).WithReturnByValue(true).Do(sessCtx)
	if err != nil {
		return "", err
	}
	if exc != nil {
		return "", fmt.Errorf("evaluate visibility: %s", exc.Text)
	}
	return visibilityFromRemoteObject(ro)
}

func (s *Session) probeVisibilityPooled(parentCtx context.Context, tabCtx context.Context) (string, error) {
	probeCtx, cancel := context.WithTimeout(parentCtx, foregroundProbeTimeout)
	defer cancel()
	c := chromedp.FromContext(tabCtx)
	if c == nil || c.Target == nil {
		return "", fmt.Errorf("tab target not available")
	}
	ro, exc, err := runtime.Evaluate(`document.visibilityState`).WithReturnByValue(true).Do(cdp.WithExecutor(probeCtx, c.Target))
	if err != nil {
		return "", err
	}
	if exc != nil {
		return "", fmt.Errorf("evaluate visibility: %s", exc.Text)
	}
	return visibilityFromRemoteObject(ro)
}

func (s *Session) probeVisibilityState(parentCtx context.Context, tabID target.ID, probe **wsProbe) (string, error) {
	if err := parentCtx.Err(); err != nil {
		return "", err
	}

	s.poolMu.Lock()
	ent, inPool := s.tabPool[tabID]
	s.poolMu.Unlock()
	if inPool {
		return s.probeVisibilityPooled(parentCtx, ent.ctx)
	}

	if *probe == nil {
		wsURL, err := browserWebSocketURL(parentCtx, s.debuggerBase)
		if err != nil {
			return "", err
		}
		p, err := dialWSProbeURL(parentCtx, wsURL)
		if err != nil {
			return "", err
		}
		*probe = p
	}
	return (*probe).tabVisibility(parentCtx, tabID)
}
