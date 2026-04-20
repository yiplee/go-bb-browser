package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AdapterMeta is minimal metadata read from an adapter file (bb-sites style).
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

// Discover scans only ~/.bb-browser/sites (recursive).
func Discover() ([]AdapterMeta, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".bb-browser", "sites")
	byName := make(map[string]AdapterMeta)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".js") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		m, err := parseMeta(raw)
		if err != nil || m.Name == "" {
			return nil
		}
		m.Path = path
		byName[m.Name] = m
		return nil
	})
	out := make([]AdapterMeta, 0, len(byName))
	for _, m := range byName {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseMeta(src []byte) (AdapterMeta, error) {
	var z AdapterMeta
	m := metaBlock.FindSubmatch(src)
	if len(m) < 2 {
		return z, fmt.Errorf("no @meta block")
	}
	if err := json.Unmarshal(m[1], &z); err != nil {
		return z, err
	}
	return z, nil
}

// ArgKeysFromAdapterSource returns @meta "args" keys in JSON source order so CLI
// positionals can match bb-sites adapters (e.g. google/search expects args.query).
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

// Search filters adapters by substring on name, description, domain.
func Search(all []AdapterMeta, query string) []AdapterMeta {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return all
	}
	var out []AdapterMeta
	for _, a := range all {
		if strings.Contains(strings.ToLower(a.Name), q) ||
			strings.Contains(strings.ToLower(a.Description), q) ||
			strings.Contains(strings.ToLower(a.Domain), q) {
			out = append(out, a)
		}
	}
	return out
}
