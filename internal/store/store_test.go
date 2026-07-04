package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestAppendRPCInMemory(t *testing.T) {
	s := openTestStore(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	body := json.RawMessage(`{"jsonrpc":"2.0","method":"tab_list","params":{},"id":1}`)

	if err := s.AppendRPC(LogRecord{
		Action:   protocol.MethodTabList,
		Body:     body,
		SenderIP: "127.0.0.1",
		Seq:      1,
		OK:       true,
		Time:     at,
	}); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	n := len(s.memLogs)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("records: %d", n)
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
	body := json.RawMessage(`{"jsonrpc":"2.0","method":"tab_new","params":{},"id":1}`)
	if err := s.AppendRPC(LogRecord{
		Action: protocol.MethodTabNew,
		Body:   body,
		Tab:    "ab12",
		Seq:    seq1,
		OK:     true,
		Time:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, rpcLogFile)
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected rpc.jsonl: %v", err)
	}
	cpPath := filepath.Join(dir, rpcCheckpointFile)
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("expected rpc-checkpoint.json: %v", err)
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
	managed := s2.ReplayManagedTabActivity()
	if managed["ab12"].IsZero() {
		t.Fatalf("managed tab not restored: %#v", managed)
	}
}

func TestCheckpointSkipsFullLogScan(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, rpcLogFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	t0 := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 2000; i++ {
		line, err := json.Marshal(LogRecord{
			Action: protocol.MethodGoto,
			Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"aa11","url":"https://ex"},"id":1}`),
			Tab:    "aa11",
			OK:     true,
			Time:   t0,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}

	cp := rpcCheckpoint{
		LogOffset: info.Size(),
		MaxSeq:    42,
		Managed: map[string]time.Time{
			"aa11": t0,
		},
	}
	if err := saveCheckpoint(filepath.Join(dir, rpcCheckpointFile), cp); err != nil {
		t.Fatal(err)
	}

	s, err := Open(OpenConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.seq.Load(); got != 42 {
		t.Fatalf("max seq: got %d want 42", got)
	}
	managed := s.ReplayManagedTabActivity()
	if len(managed) != 1 || managed["aa11"] != t0 {
		t.Fatalf("managed: %#v", managed)
	}
}

func TestReplayManagedTabActivity(t *testing.T) {
	t0 := time.Now().UTC().Add(-10 * time.Minute)
	t1 := t0.Add(2 * time.Minute)
	t2 := t1.Add(time.Minute)

	managed := make(map[string]time.Time)
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodTabNew, Tab: "aa11", Seq: 1, OK: true, Time: t0,
	})
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodGoto,
		Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"aa11"},"id":2}`),
		Tab:    "aa11", Seq: 2, OK: true, Time: t1,
	})
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodTabClose,
		Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"tab_close","params":{"tab":"aa11"},"id":3}`),
		Tab:    "aa11", Seq: 3, OK: true, Time: t2,
	})
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodTabNew, Tab: "bb22", Seq: 4, OK: true, Time: t1,
	})
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodGoto,
		Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"bb22"},"id":5}`),
		Tab:    "bb22", Seq: 5, OK: false, Error: "fail", Time: t2,
	})
	applyManagedUpdate(managed, LogRecord{
		Action: protocol.MethodGoto,
		Body:   json.RawMessage(`{"jsonrpc":"2.0","method":"goto","params":{"tab":"cc33"},"id":6}`),
		Tab:    "cc33", Seq: 6, OK: true, Time: t2,
	})

	if len(managed) != 1 {
		t.Fatalf("managed: %#v", managed)
	}
	if !managed["bb22"].Equal(t1) {
		t.Fatalf("bb22 last activity: got %v want %v", managed["bb22"], t1)
	}
}
