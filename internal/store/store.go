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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yiplee/go-bb-browser/pkg/protocol"
)

const (
	rpcLogFile = "rpc.jsonl"

	// defaultMaxLogBytes triggers rotation of rpc.jsonl once it grows past this size.
	defaultMaxLogBytes int64 = 8 << 20 // 8 MiB

	// maxLogBackups is how many rotated rpc.jsonl.N files are kept for audit.
	maxLogBackups = 3
)

// snapshotBody is the placeholder request body written for synthetic tab_new
// records emitted at rotation time (recovery only needs Action/Tab/Time).
var snapshotBody = json.RawMessage(`{"jsonrpc":"2.0","method":"tab_new","params":{}}`)

// RPCLogFile returns the on-disk RPC log filename under StateDir.
func RPCLogFile() string { return rpcLogFile }

// OpenConfig selects disk or in-memory RPC log storage.
type OpenConfig struct {
	// StateDir is the daemon state directory. "-" forces in-memory mode.
	StateDir string
	Logger   *slog.Logger
	// MaxLogBytes rotates rpc.jsonl once it exceeds this size. <=0 uses defaultMaxLogBytes.
	MaxLogBytes int64
}

// Store persists tab-related RPC lines and allocates global seq (INV-4).
type Store struct {
	mu          sync.Mutex
	logFile     *os.File
	logPath     string
	logBytes    int64
	maxLogBytes int64
	seq         atomic.Uint64
	managed     map[string]time.Time
	memLogs     []LogRecord
	logger      *slog.Logger
	inMemory    bool
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

// Open initializes rpc.jsonl on disk, or in-memory when cfg.StateDir is "" or
// "-". A configured StateDir that cannot be created or written returns an error
// rather than silently falling back to in-memory. The global seq is seeded from
// the current wall-clock nanosecond so it stays monotonic across restarts
// (INV-4) without persisting a counter.
func Open(cfg OpenConfig) (*Store, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dir := strings.TrimSpace(cfg.StateDir)
	useMemory := dir == "" || dir == "-"

	maxLogBytes := cfg.MaxLogBytes
	if maxLogBytes <= 0 {
		maxLogBytes = defaultMaxLogBytes
	}
	s := &Store{
		logger:      logger,
		inMemory:    useMemory,
		managed:     make(map[string]time.Time),
		maxLogBytes: maxLogBytes,
	}
	s.seq.Store(uint64(time.Now().UnixNano()))

	if useMemory {
		return s, nil
	}

	dir = filepath.Clean(dir)
	logPath := filepath.Join(dir, rpcLogFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir %q: %w", dir, err)
	}
	if !dirWritable(dir) {
		return nil, fmt.Errorf("state dir %q is not writable", dir)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open rpc log %q: %w", logPath, err)
	}

	managed := make(map[string]time.Time)
	if err := replayLog(f, func(rec LogRecord) error {
		applyManagedUpdate(managed, rec)
		return nil
	}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("replay rpc log: %w", err)
	}

	s.logFile = f
	s.logPath = logPath
	s.managed = managed
	if fi, err := f.Stat(); err == nil {
		s.logBytes = fi.Size()
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

// replayLog reads the full log from the start, invoking fn per record, then
// leaves the file positioned at the end for subsequent appends.
func replayLog(f *os.File, fn func(LogRecord) error) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
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

// NextSeq returns the next globally monotonic sequence number (INV-4). It is
// seeded from the wall-clock nanosecond at Open, so values keep increasing
// across restarts without persisting a counter.
func (s *Store) NextSeq() (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("store not initialized")
	}
	return s.seq.Add(1), nil
}

// AppendRPC appends one RPC log line and updates in-memory managed-tab state.
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
	applyManagedUpdate(s.managed, rec)

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
	s.logBytes += int64(len(line))
	if s.maxLogBytes > 0 && s.logBytes >= s.maxLogBytes {
		if err := s.rotateLocked(); err != nil && s.logger != nil {
			s.logger.Warn("rpc log rotation failed", "err", err)
		}
	}
	return nil
}

// rotateLocked rolls rpc.jsonl into numbered backups and starts a fresh log,
// seeding it with a snapshot of currently managed tabs so idle recovery still
// works after reading only the current file. Caller holds s.mu.
func (s *Store) rotateLocked() error {
	if s.logFile == nil {
		return nil
	}
	if err := s.logFile.Close(); err != nil && s.logger != nil {
		s.logger.Warn("closing rpc log before rotation failed", "err", err)
	}
	s.logFile = nil

	if maxLogBackups <= 0 {
		_ = os.Remove(s.logPath)
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", s.logPath, maxLogBackups))
		for k := maxLogBackups - 1; k >= 1; k-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", s.logPath, k), fmt.Sprintf("%s.%d", s.logPath, k+1))
		}
		_ = os.Rename(s.logPath, fmt.Sprintf("%s.1", s.logPath))
	}

	f, err := os.OpenFile(s.logPath, os.O_APPEND|os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	s.logFile = f
	s.logBytes = 0
	return s.writeManagedSnapshotLocked()
}

// writeManagedSnapshotLocked writes one synthetic tab_new record per managed tab
// (sorted for determinism) to the current log. Caller holds s.mu.
func (s *Store) writeManagedSnapshotLocked() error {
	if len(s.managed) == 0 {
		return nil
	}
	shorts := make([]string, 0, len(s.managed))
	for short := range s.managed {
		shorts = append(shorts, short)
	}
	sort.Strings(shorts)

	var buf bytes.Buffer
	for _, short := range shorts {
		data, err := json.Marshal(LogRecord{
			Action: protocol.MethodTabNew,
			Body:   snapshotBody,
			Tab:    short,
			OK:     true,
			Time:   s.managed[short],
		})
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	n, err := s.logFile.Write(buf.Bytes())
	s.logBytes += int64(n)
	return err
}

// ReplayManagedTabActivity returns managed tab short ids and last activity, rebuilt by replaying the log at startup.
func (s *Store) ReplayManagedTabActivity() map[string]time.Time {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneManaged(s.managed)
}
