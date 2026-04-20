package site

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseGoogleSearchArgs reads query and result count (num) from adapter args JSON.
func ParseGoogleSearchArgs(argsJSON []byte) (query string, num int, err error) {
	var m map[string]any
	if err := json.Unmarshal(argsJSON, &m); err != nil {
		return "", 0, err
	}
	qv, ok := m["query"]
	if !ok || qv == nil {
		return "", 0, fmt.Errorf("missing query")
	}
	switch v := qv.(type) {
	case string:
		query = strings.TrimSpace(v)
	default:
		query = strings.TrimSpace(fmt.Sprint(v))
	}
	if query == "" {
		return "", 0, fmt.Errorf("missing query")
	}
	num = 10
	if v, ok := m["count"]; ok && v != nil {
		switch t := v.(type) {
		case float64:
			n := int(t)
			if n > 0 {
				num = n
			}
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(t))
			if n > 0 {
				num = n
			}
		case json.Number:
			n, _ := strconv.Atoi(string(t))
			if n > 0 {
				num = n
			}
		}
	}
	return query, num, nil
}

// GoogleSearchDomEval is an async IIFE for eval after the tab has navigated to the
// Google SERP. It mirrors epiral/bb-sites google/search parsing but reads live
// document (fetch+DOMParser often gets a different HTML shape than real navigation).
const GoogleSearchDomEval = `(async function(__args){
  const args = __args && typeof __args === 'object' ? __args : {};
  if (!args.query) return {error: 'Missing argument: query', hint: 'Provide a search query string'};

  const deadline = Date.now() + 25000;
  while (Date.now() < deadline) {
    if (document.querySelector('h3')) break;
    await new Promise(r => setTimeout(r, 150));
  }

  const doc = document;
  const results = [];
  const headings = doc.querySelectorAll('h3');
  headings.forEach(h3 => {
    const anchor = h3.closest('a') || h3.parentElement?.querySelector('a[href]');
    if (!anchor) return;
    const link = anchor.getAttribute('href');
    if (!link || link.startsWith('/search') || link.startsWith('#')) return;

    let snippet = '';
    let container = h3;
    for (let i = 0; i < 5; i++) {
      container = container.parentElement;
      if (!container) break;
      const allText = container.textContent || '';
      if (allText.length > h3.textContent.length + 50) {
        const clone = container.cloneNode(true);
        const cloneH3 = clone.querySelector('h3');
        if (cloneH3) cloneH3.remove();
        clone.querySelectorAll('cite').forEach(c => c.remove());
        const remaining = clone.textContent.trim();
        if (remaining.length > 30) {
          snippet = remaining.substring(0, 300);
          break;
        }
      }
    }
    if (!snippet) {
      const parent = h3.closest('[data-ved]') || h3.parentElement?.parentElement?.parentElement;
      if (parent) {
        const spans = parent.querySelectorAll('span');
        for (const sp of spans) {
          const txt = sp.textContent.trim();
          if (txt.length > 40 && txt !== h3.textContent.trim()) {
            snippet = txt;
            break;
          }
        }
      }
    }

    results.push({
      title: h3.textContent.trim(),
      url: link.startsWith('/url?q=') ? decodeURIComponent(link.split('/url?q=')[1].split('&')[0]) : link,
      snippet: snippet
    });
  });

  const seen = new Set();
  const unique = results.filter(r => {
    if (seen.has(r.url)) return false;
    seen.add(r.url);
    return true;
  });

  return {query: args.query, count: unique.length, results: unique};
})`

// GoogleSearchDomProgram wraps GoogleSearchDomEval with args JSON for Runtime.evaluate.
func GoogleSearchDomProgram(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	return GoogleSearchDomEval + "(" + argsJSON + ")"
}
