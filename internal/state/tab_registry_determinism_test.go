package state

import (
	"testing"

	"github.com/chromedp/cdproto/target"
)

func TestSyncPageTargetsDeterministicShortIDs(t *testing.T) {
	r := NewTabRegistry()
	infos := []*target.Info{
		{TargetID: "BBBBBBBB2222EEEE", Type: "page"},
		{TargetID: "AAAAAAAA1111CCCC", Type: "page"},
	}
	var first []string
	for range 10 {
		r = NewTabRegistry()
		snap := r.SyncPageTargets(infos)
		if len(snap) != 2 {
			t.Fatalf("len %d", len(snap))
		}
		shorts := []string{snap[0].ShortID, snap[1].ShortID}
		if first == nil {
			first = shorts
			continue
		}
		if first[0] != shorts[0] || first[1] != shorts[1] {
			t.Fatalf("unstable shorts: %#v vs %#v", first, shorts)
		}
	}
}
