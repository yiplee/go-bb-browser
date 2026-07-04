package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const rpcCheckpointFile = "rpc-checkpoint.json"

// RPCCheckpointFile returns the idle-recovery checkpoint filename under StateDir.
func RPCCheckpointFile() string { return rpcCheckpointFile }

type rpcCheckpoint struct {
	LogOffset int64                `json:"log_offset"`
	MaxSeq    uint64               `json:"max_seq"`
	Managed   map[string]time.Time `json:"managed"`
}

func loadCheckpoint(path string) (rpcCheckpoint, error) {
	var cp rpcCheckpoint
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cp, nil
		}
		return cp, err
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, err
	}
	if cp.Managed == nil {
		cp.Managed = make(map[string]time.Time)
	}
	return cp, nil
}

func saveCheckpoint(path string, cp rpcCheckpoint) error {
	if cp.Managed == nil {
		cp.Managed = make(map[string]time.Time)
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, rpcCheckpointFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
