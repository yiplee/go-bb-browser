package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
)

func TestTabStateStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := newTabStateStore(dir)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	id := target.ID("ABCDEF123456")
	in := map[target.ID]time.Time{id: at}

	if err := store.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Load len=%d want 1", len(got))
	}
	if !got[id].Equal(at) {
		t.Fatalf("Load[%s]=%v want %v", id, got[id], at)
	}
}

func TestTabStateStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := newTabStateStore(dir)
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Load=%#v want empty", got)
	}
}

func TestEffectiveStateDirDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got := effectiveStateDir(Config{})
	want := filepath.Join(home, defaultStateDirRel)
	if got != want {
		t.Fatalf("effectiveStateDir=%q want %q", got, want)
	}
}

func TestEffectiveStateDirDisabled(t *testing.T) {
	if got := effectiveStateDir(Config{StateDir: stateDirDisabled}); got != "" {
		t.Fatalf("effectiveStateDir=%q want empty", got)
	}
}
