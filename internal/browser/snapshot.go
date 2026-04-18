package browser

import (
	"encoding/json"
	"fmt"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const snapshotStub = `
(function(opts){
opts = opts || {};
var interactiveOnly = !!opts.interactiveOnly;
var pruneEmpty = !!opts.pruneEmpty;
var maxDepth = opts.maxDepth ? Number(opts.maxDepth) : 0;
var scopeSel = opts.selectorScope ? String(opts.selectorScope) : '';

function trim(s){ return (s||'').replace(/\s+/g,' ').trim(); }

function isInteractive(el){
  var t = el.tagName;
  if (t === 'BUTTON' || t === 'A' || t === 'SELECT' || t === 'TEXTAREA') return true;
  if (t === 'INPUT') return true;
  var role = el.getAttribute && el.getAttribute('role');
  if (role === 'button' || role === 'link' || role === 'checkbox' || role === 'radio' ||
      role === 'option' || role === 'menuitem') return true;
  return false;
}

function visible(el){
  if (!el.getBoundingClientRect) return false;
  var r = el.getBoundingClientRect();
  if (!r.width && !r.height) return false;
  var st = window.getComputedStyle(el);
  if (st.visibility === 'hidden' || st.display === 'none' || Number(st.opacity) === 0) return false;
  return true;
}

function attrs(el){
  var tag = el.tagName.toLowerCase();
  var bits = [tag];
  if (el.type) bits.push('type="'+String(el.type)+'"');
  if (el.name) bits.push('name="'+String(el.name)+'"');
  if (el.placeholder) bits.push('placeholder="'+String(el.placeholder)+'"');
  if (tag === 'a' && el.href) bits.push('href="'+String(el.href)+'"');
  return '[' + bits.join(' ') + ']';
}

function snapshotEl(el, depth, counter){
  if (!el || !el.tagName) return null;
  var tag = el.tagName.toLowerCase();
  if (tag === 'script' || tag === 'style' || tag === 'noscript') return null;
  if (!visible(el)) return null;
  if (maxDepth && depth > maxDepth) return null;

  var childSnaps = [];
  var ch = el.children || [];
  for (var i = 0; i < ch.length; i++) {
    var cs = snapshotEl(ch[i], depth + 1, counter);
    if (cs) childSnaps.push(cs);
  }

  var ia = isInteractive(el);
  if (interactiveOnly && !ia && childSnaps.length === 0) return null;

  var txt = trim(el.innerText||'');
  var label = '';
  if (txt.length > 120) txt = txt.slice(0,117)+'...';
  if (tag === 'input' || tag === 'textarea') {
    if (el.value) label = trim(String(el.value)).slice(0,80);
  }
  if (!label && txt) label = txt;

  var entry = null;
  if (!(pruneEmpty && childSnaps.length===0 && !ia && !label)) {
    counter.n++;
    var id = counter.n;
    var attrName = '__bb_snap_ref';
    el.setAttribute(attrName, String(id));
    entry = {
      id: id,
      lineDepth: depth,
      head: '@' + id + ' ' + attrs(el) + (label ? ' ' + JSON.stringify(label) : ''),
      selector: '[' + attrName + '="' + String(id).replace(/"/g,'\\"') + '"]'
    };
  }

  return { entry: entry, kids: childSnaps };
}

function flatten(tree, lines, refs){
  if (!tree) return;
  if (tree.entry){
    var pad = '';
    for (var i=0;i<tree.entry.lineDepth;i++) pad += '  ';
    lines.push(pad + tree.entry.head);
    refs[String(tree.entry.id)] = tree.entry.selector;
  }
  for (var j=0;j<tree.kids.length;j++) flatten(tree.kids[j], lines, refs);
}

var root = scopeSel ? document.querySelector(scopeSel) : document.body;
if (!root) {
  return { title: document.title||'', url: location.href, text: '', refs: {} };
}
var counter = { n: 0 };
var tree = snapshotEl(root, 0, counter);
var lines = [];
var refs = {};
flatten(tree, lines, refs);
var text = '页面: ' + (document.title||'') + '\nURL: ' + location.href + '\n\n' + lines.join('\n');
return { title: document.title||'', url: location.href, text: text, refs: refs };
})(%s)
`

// SnapshotOpts configures compact DOM snapshot for @ref → selector mapping.
type SnapshotOpts struct {
	InteractiveOnly bool
	PruneEmpty      bool
	MaxDepth        int
	SelectorScope   string
}

type snapshotPayload struct {
	Title string            `json:"title"`
	URL   string            `json:"url"`
	Text  string            `json:"text"`
	Refs  map[string]string `json:"refs"`
}

// Snapshot captures a compact tree snapshot and assigns __bb_snap_ref attributes for selectors.
func (s *Session) Snapshot(tabID target.ID, opts SnapshotOpts) (title, url, text string, refs map[string]string, err error) {
	if s == nil {
		return "", "", "", nil, errNilSession()
	}
	tabCtx, err := s.tabChromeCtx(tabID)
	if err != nil {
		return "", "", "", nil, err
	}
	optMap := map[string]any{
		"interactiveOnly": opts.InteractiveOnly,
		"pruneEmpty":      opts.PruneEmpty,
		"selectorScope":   opts.SelectorScope,
	}
	if opts.MaxDepth > 0 {
		optMap["maxDepth"] = opts.MaxDepth
	}
	optJSON, err := json.Marshal(optMap)
	if err != nil {
		return "", "", "", nil, err
	}
	expr := fmt.Sprintf(snapshotStub, string(optJSON))

	var raw []byte
	err = chromedp.Run(tabCtx, chromedp.Evaluate(expr, &raw))
	if err != nil {
		return "", "", "", nil, err
	}
	var p snapshotPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", "", nil, err
	}
	if p.Refs == nil {
		p.Refs = map[string]string{}
	}
	return p.Title, p.URL, p.Text, p.Refs, nil
}
