package state

import (
	"testing"

	"github.com/chromedp/cdproto/target"
)

func TestShortIDFromSuffix(t *testing.T) {
	r := NewTabRegistry()
	infos := []*target.Info{
		{TargetID: "ABCDEF123456", Type: "page", Title: "a", URL: "https://a"},
	}
	snap := r.SyncPageTargets(infos)
	if len(snap) != 1 {
		t.Fatalf("snap len %d", len(snap))
	}
	// Last 4 hex chars of id: ...3456
	if snap[0].ShortID != "3456" {
		t.Fatalf("short id %q want 3456", snap[0].ShortID)
	}
}

func TestShortIDCollisionLengthens(t *testing.T) {
	r := NewTabRegistry()
	// Two ids that share the same last 4 hex digits
	infos := []*target.Info{
		{TargetID: "1111AAAA2222BBBB", Type: "page", Title: "1", URL: ""},
		{TargetID: "9999CCCC2222BBBB", Type: "page", Title: "2", URL: ""},
	}
	snap := r.SyncPageTargets(infos)
	if len(snap) != 2 {
		t.Fatalf("snap len %d", len(snap))
	}
	shorts := map[string]struct{}{}
	for _, s := range snap {
		shorts[s.ShortID] = struct{}{}
	}
	if len(shorts) != 2 {
		t.Fatalf("expected distinct short ids, got %#v", snap)
	}
}

func TestLookupInvalidTab(t *testing.T) {
	r := NewTabRegistry()
	_, ok := r.Lookup("nope")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestSelectUnknownTab(t *testing.T) {
	r := NewTabRegistry()
	if r.Select("abcd") {
		t.Fatal("select should fail for unknown tab")
	}
}

func TestShortIDCollisionUsesMoreDigits(t *testing.T) {
	r := NewTabRegistry()
	infos := []*target.Info{
		{TargetID: "AAA11115678", Type: "page"},
		{TargetID: "BBB22225678", Type: "page"},
	}
	snap := r.SyncPageTargets(infos)
	if len(snap) != 2 {
		t.Fatalf("snap len %d", len(snap))
	}
	shorts := map[string]struct{}{}
	for _, s := range snap {
		shorts[s.ShortID] = struct{}{}
	}
	if len(shorts) != 2 {
		t.Fatalf("want distinct shorts, got %#v", snap)
	}
}
