package site

import (
	"path/filepath"
	"testing"
)

func TestParseMeta(t *testing.T) {
	src := []byte(`/* @meta
{"name":"demo/x","description":"d","domain":"example.com","args":{"q":{"required":true}}}
*/
`)
	m, err := parseMeta(src)
	if err != nil || m.Name != "demo/x" || m.Domain != "example.com" {
		t.Fatalf("meta %+v err %v", m, err)
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "nosuch"))
	_, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
}
