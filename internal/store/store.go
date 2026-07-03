package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
)

const (
	seqKey      = "meta/seq"
	auditSeqKey = "meta/audit_id"
	seqBandwidth  = uint64(100)

	auditKeyPrefix = "audit/"
	tabKeyPrefix   = "tab/"
)

// OpenConfig selects disk or in-memory Badger storage.
type OpenConfig struct {
	// StateDir is the daemon state directory. "-" forces in-memory mode.
	StateDir string
	Logger   *slog.Logger
}

// Store is a Badger-backed persistence layer for global seq, RPC audit, and managed tabs.
type Store struct {
	db        *badger.DB
	globalSeq *badger.Sequence
	auditSeq  *badger.Sequence
	logger    *slog.Logger
	inMemory  bool
}

// AuditRecord is a compact summary of one persisted RPC call.
type AuditRecord struct {
	ID       uint64    `json:"id"`
	Action   string    `json:"action"`
	Tab      string    `json:"tab,omitempty"`
	SenderIP string    `json:"sender_ip"`
	Seq      uint64    `json:"seq,omitempty"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Time     time.Time `json:"time"`
}

// TabRecord is persisted metadata for a daemon-created tab.
type TabRecord struct {
	TargetID       string    `json:"target_id"`
	ShortID        string    `json:"short_id"`
	OpenURL        string    `json:"open_url"`
	OpenedAt       time.Time `json:"opened_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	Silent         bool      `json:"silent,omitempty"`
}

// Open initializes Badger on disk or in-memory per cfg.StateDir.
func Open(cfg OpenConfig) (*Store, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dir := strings.TrimSpace(cfg.StateDir)
	useMemory := dir == "" || dir == "-"

	var opts badger.Options
	if useMemory {
		opts = badger.DefaultOptions("").WithInMemory(true)
	} else {
		dir = filepath.Clean(dir)
		badgerDir := filepath.Join(dir, "badger")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Warn("badger falling back to in-memory", "dir", dir, "err", err)
			opts = badger.DefaultOptions("").WithInMemory(true)
			useMemory = true
		} else if !dirWritable(dir) {
			logger.Warn("badger falling back to in-memory", "dir", dir, "err", "directory not writable")
			opts = badger.DefaultOptions("").WithInMemory(true)
			useMemory = true
		} else {
			opts = badger.DefaultOptions(badgerDir)
		}
	}

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("badger open: %w", err)
	}

	globalSeq, err := db.GetSequence([]byte(seqKey), seqBandwidth)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("badger global seq: %w", err)
	}
	auditSeq, err := db.GetSequence([]byte(auditSeqKey), seqBandwidth)
	if err != nil {
		_ = globalSeq.Release()
		_ = db.Close()
		return nil, fmt.Errorf("badger audit seq: %w", err)
	}

	return &Store{
		db:        db,
		globalSeq: globalSeq,
		auditSeq:  auditSeq,
		logger:    logger,
		inMemory:  useMemory,
	}, nil
}

func dirWritable(dir string) bool {
	testPath := filepath.Join(dir, ".write-test")
	if err := os.WriteFile(testPath, []byte("1"), 0o644); err != nil {
		return false
	}
	_ = os.Remove(testPath)
	return true
}

// InMemory reports whether the store uses Badger in-memory mode.
func (s *Store) InMemory() bool {
	if s == nil {
		return true
	}
	return s.inMemory
}

// Close releases Badger resources.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.globalSeq != nil {
		_ = s.globalSeq.Release()
		s.globalSeq = nil
	}
	if s.auditSeq != nil {
		_ = s.auditSeq.Release()
		s.auditSeq = nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// NextSeq returns the next globally monotonic sequence number (INV-4), starting at 1.
func (s *Store) NextSeq() (uint64, error) {
	if s == nil || s.globalSeq == nil {
		return 0, fmt.Errorf("store not initialized")
	}
	n, err := s.globalSeq.Next()
	if err != nil {
		return 0, err
	}
	return n + 1, nil
}

func (s *Store) nextAuditID() (uint64, error) {
	if s == nil || s.auditSeq == nil {
		return 0, fmt.Errorf("store not initialized")
	}
	n, err := s.auditSeq.Next()
	if err != nil {
		return 0, err
	}
	return n + 1, nil
}

// NextAuditID allocates the next audit record id (for synchronous assignment before async write).
func (s *Store) NextAuditID() (uint64, error) {
	return s.nextAuditID()
}

func auditKey(id uint64) []byte {
	return []byte(fmt.Sprintf("%s%020d", auditKeyPrefix, id))
}

func tabKey(targetID string) []byte {
	return []byte(tabKeyPrefix + targetID)
}

// AppendAudit persists one RPC audit record (request/response should already be sanitized).
func (s *Store) AppendAudit(rec AuditRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if rec.ID == 0 {
		return fmt.Errorf("audit record id required")
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now().UTC()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := auditKey(rec.ID)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// ListAudit returns audit records with id > since, up to limit.
func (s *Store) ListAudit(since uint64, limit int) ([]AuditRecord, uint64, error) {
	if s == nil || s.db == nil {
		return nil, 0, fmt.Errorf("store not initialized")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var out []AuditRecord
	var cursor uint64

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(auditKeyPrefix)
		start := auditKey(since + 1)
		for it.Seek(start); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			var rec AuditRecord
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &rec)
			}); err != nil {
				return err
			}
			if rec.ID <= since {
				continue
			}
			out = append(out, rec)
			if rec.ID > cursor {
				cursor = rec.ID
			}
			if len(out) >= limit {
				break
			}
		}
		return nil
	})
	return out, cursor, err
}

// PutTab inserts or replaces a managed tab record.
func (s *Store) PutTab(rec TabRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if rec.TargetID == "" {
		return fmt.Errorf("tab target_id required")
	}
	if rec.OpenedAt.IsZero() {
		rec.OpenedAt = time.Now().UTC()
	}
	if rec.LastActivityAt.IsZero() {
		rec.LastActivityAt = rec.OpenedAt
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(tabKey(rec.TargetID), data)
	})
}

// TouchTab updates last_activity_at for a managed tab.
func (s *Store) TouchTab(targetID string, at time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if targetID == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(tabKey(targetID))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil
			}
			return err
		}
		var rec TabRecord
		if err := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &rec)
		}); err != nil {
			return err
		}
		rec.LastActivityAt = at
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return txn.Set(tabKey(targetID), data)
	})
}

// DeleteTab removes a managed tab record.
func (s *Store) DeleteTab(targetID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if targetID == "" {
		return nil
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(tabKey(targetID))
	})
}

// ListTabs returns all managed tab records.
func (s *Store) ListTabs() ([]TabRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	var out []TabRecord
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(tabKeyPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			var rec TabRecord
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &rec)
			}); err != nil {
				return err
			}
			out = append(out, rec)
		}
		return nil
	})
	return out, err
}
