package site

import (
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

// Discover scans ~/.bb-browser/sites and ~/.bb-browser/bb-sites (private wins on name).
func Discover() ([]AdapterMeta, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	base := filepath.Join(home, ".bb-browser")
	dirs := []string{
		filepath.Join(base, "sites"),
		filepath.Join(base, "bb-sites"),
	}
	byName := make(map[string]AdapterMeta)
	for _, dir := range dirs {
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
	}
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
