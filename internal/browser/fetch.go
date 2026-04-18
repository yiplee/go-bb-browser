package browser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// FetchPage runs fetch() in page context with credentials included; resolves relative URLs against the document.
func (s *Session) FetchPage(tabID target.ID, rawURL, method string, headersJSON []byte, body string) (json.RawMessage, error) {
	if s == nil {
		return nil, errNilSession()
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return nil, err
	}
	rawQ, err := json.Marshal(rawURL)
	if err != nil {
		return nil, err
	}
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = "GET"
	}
	methQ, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	h := headersJSON
	if len(h) == 0 || string(h) == "null" {
		h = []byte("{}")
	}
	bodyQ, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	expr := fmt.Sprintf(`
(async function(){
  const raw = %s;
  const method = %s;
  let hdrs = %s;
  const bod = %s;
  try {
    if (typeof hdrs === 'string') { hdrs = JSON.parse(hdrs); }
  } catch (e) {
    return { ok: false, error: 'invalid headers JSON: ' + String(e) };
  }
  try {
    const url = new URL(raw, location.href).href;
    const init = { method, credentials: 'include', redirect: 'follow' };
    const hm = hdrs && typeof hdrs === 'object' ? hdrs : {};
    const headerObj = new Headers(hm);
    init.headers = headerObj;
    if (bod !== undefined && bod !== null && bod !== '' &&
        !(method === 'GET' || method === 'HEAD')) {
      init.body = bod;
    }
    const resp = await fetch(url, init);
    const ct = resp.headers.get('content-type') || '';
    let text = '';
    try { text = await resp.text(); } catch (e) {}
    let parsed = null;
    try {
      if (ct.includes('application/json')) parsed = JSON.parse(text);
    } catch (e) {}
    return {
      ok: resp.ok,
      status: resp.status,
      statusText: resp.statusText,
      url: resp.url,
      contentType: ct,
      bodyText: parsed === null ? text : undefined,
      json: parsed !== null ? parsed : undefined
    };
  } catch (e) {
    return { ok: false, error: String(e && e.message ? e.message : e) };
  }
})()`, string(rawQ), string(methQ), string(h), string(bodyQ))

	var raw []byte
	err = chromedp.Run(tabCtx, chromedp.Evaluate(expr, &raw, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true).WithReturnByValue(true)
	}))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
