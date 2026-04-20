package guidebook

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Skill docs are mirrored from ../../skills/bb-browser (Go embed cannot reference paths outside the module package tree).
//
//go:embed all:files/bb-browser
var files embed.FS

// DefaultTopic is the main SKILL.md document.
const DefaultTopic = "skill"

// TopicNames returns embedded reference names (without .md) plus "skill".
func TopicNames() ([]string, error) {
	var refs []string
	entries, err := fs.ReadDir(files, path.Join("files", "bb-browser", "references"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".md") {
			refs = append(refs, strings.TrimSuffix(n, ".md"))
		}
	}
	sort.Strings(refs)
	return append([]string{DefaultTopic}, refs...), nil
}

// Read returns markdown bytes for a topic key: "skill" for SKILL.md, or a base name like "site-system".
func Read(topic string) ([]byte, error) {
	t := strings.TrimSpace(strings.ToLower(topic))
	if t == "" || t == DefaultTopic {
		return files.ReadFile(path.Join("files", "bb-browser", "SKILL.md"))
	}
	base := strings.TrimSuffix(t, ".md")
	p := path.Join("files", "bb-browser", "references", base+".md")
	return files.ReadFile(p)
}
