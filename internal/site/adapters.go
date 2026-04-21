package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// AdapterMeta is minimal metadata read from an adapter file (@meta JSON in JS).
type AdapterMeta struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	Args        map[string]ArgDef `json:"args"`
	Example     string            `json:"example"`
	Path        string            `json:"-"`
}

// ArgDef describes one adapter argument in @meta.
type ArgDef struct {
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

var metaBlock = regexp.MustCompile(`(?s)/\*\s*@meta\s*(\{.*?\})\s*\*/`)

// ErrNoMeta means the script has no @meta block.
var ErrNoMeta = errors.New("no @meta block")

// ReadAdapterFile reads a JS adapter from path and parses @meta when present.
// Missing @meta returns empty AdapterMeta and no error.
func ReadAdapterFile(path string) ([]byte, AdapterMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, AdapterMeta{}, err
	}
	meta, err := parseMeta(raw)
	if errors.Is(err, ErrNoMeta) {
		return raw, AdapterMeta{}, nil
	}
	if err != nil {
		return raw, AdapterMeta{}, err
	}
	return raw, meta, nil
}

func parseMeta(src []byte) (AdapterMeta, error) {
	var z AdapterMeta
	m := metaBlock.FindSubmatch(src)
	if len(m) < 2 {
		return z, ErrNoMeta
	}
	if err := json.Unmarshal(m[1], &z); err != nil {
		return z, err
	}
	return z, nil
}

// ArgKeysFromAdapterSource returns @meta "args" keys in JSON source order so CLI
// positionals map to @meta "args" keys in JSON source order, then arg1, arg2, …
func ArgKeysFromAdapterSource(src []byte) []string {
	m := metaBlock.FindSubmatch(src)
	if len(m) < 2 {
		return nil
	}
	keys, err := metaArgKeysInOrder(m[1])
	if err != nil {
		return nil
	}
	return keys
}

func metaArgKeysInOrder(metaInner []byte) ([]string, error) {
	var meta struct {
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(metaInner, &meta); err != nil {
		return nil, err
	}
	if len(meta.Args) == 0 || string(meta.Args) == "null" {
		return nil, nil
	}
	return argsObjectKeysInOrder(meta.Args)
}

// argsObjectKeysInOrder walks a JSON object using Decoder.Token + Decode
// (Decoder.Skip is unavailable in some Go versions).
func argsObjectKeysInOrder(objJSON []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(objJSON))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return nil, fmt.Errorf("args: expected object")
	}
	var keys []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if delim, ok := tok.(json.Delim); ok && delim == '}' {
			return keys, nil
		}
		k, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("args: expected string key")
		}
		keys = append(keys, k)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
	}
}
