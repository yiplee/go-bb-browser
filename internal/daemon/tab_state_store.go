package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
)

const tabStateFileName = "tabs.json"

type tabStateStore struct {
	path string
	mu   sync.Mutex
}

func newTabStateStore(dir string) *tabStateStore {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil
	}
	return &tabStateStore{path: filepath.Join(dir, tabStateFileName)}
}

func (st *tabStateStore) Load() (map[target.ID]time.Time, error) {
	if st == nil {
		return nil, nil
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[target.ID]time.Time{}, nil
		}
		return nil, err
	}
	var raw map[string]int64
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make(map[target.ID]time.Time, len(raw))
	for k, v := range raw {
		if k == "" {
			continue
		}
		out[target.ID(k)] = time.Unix(0, v)
	}
	return out, nil
}

func (st *tabStateStore) Save(managed map[target.ID]time.Time) error {
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	raw := make(map[string]int64, len(managed))
	for id, at := range managed {
		if id == "" {
			continue
		}
		raw[string(id)] = at.UnixNano()
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tabs-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, st.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func initTabStateStore(dir string, logger interface {
	Warn(msg string, args ...any)
}) *tabStateStore {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if logger != nil {
			logger.Warn("tab state persistence disabled", "dir", dir, "err", err)
		}
		return nil
	}
	testPath := filepath.Join(dir, ".write-test")
	if err := os.WriteFile(testPath, []byte("1"), 0o644); err != nil {
		if logger != nil {
			logger.Warn("tab state persistence disabled", "dir", dir, "err", err)
		}
		return nil
	}
	_ = os.Remove(testPath)
	return newTabStateStore(dir)
}
