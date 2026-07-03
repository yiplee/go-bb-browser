package store

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(OpenConfig{StateDir: "-"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNextSeqMonotonic(t *testing.T) {
	s := openTestStore(t)
	var last uint64
	for i := 0; i < 100; i++ {
		n, err := s.NextSeq()
		if err != nil {
			t.Fatal(err)
		}
		if n <= last {
			t.Fatalf("seq not increasing: got %d after %d", n, last)
		}
		last = n
	}
}

func TestNextSeqConcurrentUnique(t *testing.T) {
	s := openTestStore(t)
	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	seen := make(map[uint64]struct{})
	var mu sync.Mutex

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				n, err := s.NextSeq()
				if err != nil {
					t.Errorf("seq: %v", err)
					return
				}
				mu.Lock()
				if _, dup := seen[n]; dup {
					mu.Unlock()
					t.Errorf("duplicate seq %d", n)
					return
				}
				seen[n] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != workers*iters {
		t.Fatalf("unique seq count: got %d want %d", len(seen), workers*iters)
	}
}

func TestAuditAppendList(t *testing.T) {
	s := openTestStore(t)
	body := json.RawMessage(`{"jsonrpc":"2.0","method":"tab_list","id":1}`)
	resp := json.RawMessage(`{"jsonrpc":"2.0","result":{"seq":1},"id":1}`)
	at := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.AppendAudit(AuditRecord{
		ID:       1,
		Action:   protocol.MethodTabList,
		Body:     body,
		SenderIP: "127.0.0.1",
		Time:     at,
		Response: resp,
	}); err != nil {
		t.Fatal(err)
	}

	recs, cursor, err := s.ListAudit(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records: %d", len(recs))
	}
	if recs[0].Action != protocol.MethodTabList {
		t.Fatalf("action: %q", recs[0].Action)
	}
	if cursor != recs[0].ID {
		t.Fatalf("cursor %d id %d", cursor, recs[0].ID)
	}

	recs2, _, err := s.ListAudit(cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs2) != 0 {
		t.Fatalf("expected no more records, got %d", len(recs2))
	}
}

func TestListAuditSeekByID(t *testing.T) {
	s := openTestStore(t)
	for i := uint64(1); i <= 3; i++ {
		if err := s.AppendAudit(AuditRecord{ID: i, Action: protocol.MethodTabList}); err != nil {
			t.Fatal(err)
		}
	}
	recs, cursor, err := s.ListAudit(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || cursor != 3 {
		t.Fatalf("records %#v cursor %d", recs, cursor)
	}
}

func TestTabCRUD(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := TabRecord{
		TargetID:       "ABCDEF123456",
		ShortID:        "a1",
		OpenURL:        "https://example.com",
		OpenedAt:       now,
		LastActivityAt: now,
		Silent:         true,
	}
	if err := s.PutTab(rec); err != nil {
		t.Fatal(err)
	}

	tabs, err := s.ListTabs()
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].ShortID != "a1" {
		t.Fatalf("list: %#v", tabs)
	}

	later := now.Add(time.Minute)
	if err := s.TouchTab(rec.TargetID, later); err != nil {
		t.Fatal(err)
	}
	tabs, err = s.ListTabs()
	if err != nil {
		t.Fatal(err)
	}
	if !tabs[0].LastActivityAt.Equal(later) {
		t.Fatalf("touch: got %v want %v", tabs[0].LastActivityAt, later)
	}

	if err := s.DeleteTab(rec.TargetID); err != nil {
		t.Fatal(err)
	}
	tabs, err = s.ListTabs()
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 0 {
		t.Fatalf("expected empty, got %#v", tabs)
	}
}

func TestOpenDiskMode(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(OpenConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s.InMemory() {
		t.Fatal("expected disk mode")
	}
	seq1, err := s.NextSeq()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(OpenConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	seq2, err := s2.NextSeq()
	if err != nil {
		t.Fatal(err)
	}
	if seq2 <= seq1 {
		t.Fatalf("seq not persisted: %d after %d", seq2, seq1)
	}
}
