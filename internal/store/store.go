package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const rpcLogFile = "rpc.jsonl"

// RPCLogFile returns the on-disk RPC log filename under StateDir.
func RPCLogFile() string { return rpcLogFile }

// OpenConfig selects disk or in-memory RPC log storage.
type OpenConfig struct {
	// StateDir is the daemon state directory. "-" forces in-memory mode.
	StateDir string
	Logger   *slog.Logger
}

// Store persists tab-related RPC lines, idle checkpoint, and allocates global seq (INV-4).
type Store struct {
	mu             sync.Mutex
	logFile        *os.File
	logPath        string
	checkpointPath string
	seq            atomic.Uint64
	managed        map[string]time.Time
	memLogs        []LogRecord
	logger         *slog.Logger
	inMemory       bool
}

// LogRecord is one append-only RPC log line.
type LogRecord struct {
	Action   string          `json:"action"`
	Body     json.RawMessage `json:"body"`
	Tab      string          `json:"tab,omitempty"`
	SenderIP string          `json:"sender_ip"`
	Seq      uint64          `json:"seq,omitempty"`
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Time     time.Time       `json:"time"`
}

// Open initializes rpc.jsonl and rpc-checkpoint.json on disk, or in-memory per cfg.StateDir.
func Open(cfg OpenConfig) (*Store, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dir := strings.TrimSpace(cfg.StateDir)
	useMemory := dir == "" || dir == "-"

	s := &Store{
		logger:   logger,
		inMemory: useMemory,
		managed:  make(map[string]time.Time),
	}

	if useMemory {
		return s, nil
	}

	dir = filepath.Clean(dir)
	logPath := filepath.Join(dir, rpcLogFile)
	checkpointPath := filepath.Join(dir, rpcCheckpointFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Warn("rpc log falling back to in-memory", "dir", dir, "err", err)
		s.inMemory = true
		return s, nil
	}
	if !dirWritable(dir) {
		logger.Warn("rpc log falling back to in-memory", "dir", dir, "err", "directory not writable")
		s.inMemory = true
		return s, nil
	}

	cp, cpErr := loadCheckpoint(checkpointPath)
	checkpointExists := cpErr == nil
	if cpErr != nil && !os.IsNotExist(cpErr) {
		logger.Warn("rpc checkpoint unreadable, scanning full log", "err", cpErr)
		cp = rpcCheckpoint{}
		checkpointExists = false
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		logger.Warn("rpc log falling back to in-memory", "path", logPath, "err", err)
		s.inMemory = true
		return s, nil
	}

	managed := cloneManaged(cp.Managed)
	maxSeq := cp.MaxSeq
	scanFrom := cp.LogOffset
	if !checkpointExists {
		scanFrom = 0
	}
	if err := scanLogTail(f, scanFrom, func(rec LogRecord) error {
		applyManagedFromLogLine(managed, rec)
		return nil
	}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("scan rpc log tail: %w", err)
	}

	s.logFile = f
	s.logPath = logPath
	s.checkpointPath = checkpointPath
	s.managed = managed
	if maxSeq > 0 {
		s.seq.Store(maxSeq)
	}

	if offset, err := f.Seek(0, io.SeekEnd); err == nil && offset > 0 && (!checkpointExists || cp.LogOffset == 0) {
		if err := saveCheckpoint(checkpointPath, rpcCheckpoint{
			LogOffset: offset,
			MaxSeq:    s.seq.Load(),
			Managed:   cloneManaged(managed),
		}); err != nil && logger != nil {
			logger.Warn("write initial rpc checkpoint failed", "err", err)
		}
	}
	return s, nil
}

func dirWritable(dir string) bool {
	testPath := filepath.Join(dir, ".write-test")
	if err := os.WriteFile(testPath, []byte("1"), 0o644); err != nil {
		return false
	}
	_ = os.Remove(testPath)
	return true
}

func scanLogTail(f *os.File, offset int64, fn func(LogRecord) error) error {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	defer func() { _, _ = f.Seek(0, io.SeekEnd) }()

	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				var rec LogRecord
				if err := json.Unmarshal(trimmed, &rec); err != nil {
					return fmt.Errorf("parse log line: %w", err)
				}
				if err := fn(rec); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// InMemory reports whether the store uses in-memory mode (no disk persistence).
func (s *Store) InMemory() bool {
	if s == nil {
		return true
	}
	return s.inMemory
}

// Close releases store resources.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logFile != nil {
		err := s.logFile.Close()
		s.logFile = nil
		return err
	}
	return nil
}

// NextSeq returns the next globally monotonic sequence number (INV-4), starting at 1.
func (s *Store) NextSeq() (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("store not initialized")
	}
	return s.seq.Add(1), nil
}

// AppendRPC appends one RPC log line and updates idle checkpoint.
func (s *Store) AppendRPC(rec LogRecord) error {
	if s == nil {
		return fmt.Errorf("store not initialized")
	}
	if rec.Action == "" {
		return fmt.Errorf("log record action required")
	}
	if len(rec.Body) == 0 {
		return fmt.Errorf("log record body required")
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now().UTC()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line := append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.managed == nil {
		s.managed = make(map[string]time.Time)
	}
	if seq := applyManagedUpdate(s.managed, rec); seq > s.seq.Load() {
		s.seq.Store(seq)
	}

	if s.inMemory {
		s.memLogs = append(s.memLogs, rec)
		return nil
	}
	if s.logFile == nil {
		return fmt.Errorf("log file not open")
	}
	if _, err := s.logFile.Write(line); err != nil {
		return err
	}
	offset, err := s.logFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	return saveCheckpoint(s.checkpointPath, rpcCheckpoint{
		LogOffset: offset,
		MaxSeq:    s.seq.Load(),
		Managed:   cloneManaged(s.managed),
	})
}

// ReplayManagedTabActivity returns managed tab short ids and last activity (from checkpoint + tail replay).
func (s *Store) ReplayManagedTabActivity() map[string]time.Time {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneManaged(s.managed)
}
