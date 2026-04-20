package site

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestArgKeysFromAdapterSource_order(t *testing.T) {
	src := []byte(`/* @meta
{
 "name": "google/search",
 "args": {
 "query": {"required": true},
 "count": {"required": false}
 }
}
*/
`)
	got := ArgKeysFromAdapterSource(src)
	if len(got) != 2 || got[0] != "query" || got[1] != "count" {
		t.Fatalf("keys %v", got)
	}
}

func TestArgKeysFromAdapterSource_missingArgs(t *testing.T) {
	src := []byte(`/* @meta {"name":"x","domain":"d.example"} */
`)
	if ArgKeysFromAdapterSource(src) != nil {
		t.Fatal("expected nil")
	}
}

func TestArgsObject_metaPositional(t *testing.T) {
	b, err := ArgsObject([]string{"bitcoin"}, nil, []string{"query", "count"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"query":"bitcoin"`) || !strings.Contains(string(b), `"arg1":"bitcoin"`) {
		t.Fatalf("%s", b)
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "nosuch"))
	_, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrepareAdapterJS_anonymousAsync(t *testing.T) {
	src := []byte(`/* @meta
{"name":"demo/x","description":"d","domain":"example.com"}
*/
async function(args) {
  const x = "}";
  if (1) { return {ok: true}; }
  return {a: 1};
}
`)
	out := string(prepareAdapterJS(src))
	if !strings.Contains(out, "const __bb_run = async function(args)") || !strings.Contains(out, "return await __bb_run(__args)") {
		t.Fatalf("expected __bb_run wrapper, got:\n%s", out)
	}
}

func TestPrepareAdapterJS_namedAsyncUnchanged(t *testing.T) {
	src := []byte(`/* @meta {"name":"x"} */
async function main(args) {
  return 1;
}
`)
	out := string(prepareAdapterJS(src))
	if strings.Contains(out, "__bb_run") {
		t.Fatalf("named async should not be rewritten, got:\n%s", out)
	}
}

func TestRunScript_nodeSyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not in PATH")
	}
	src := []byte(`/* @meta {} */
async function(args) { return {n: 1}; }
`)
	js := RunScript(src, "{}")
	cmd := exec.Command("node", "--check")
	cmd.Stdin = bytes.NewReader([]byte(js))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --check: %v\n%s\nscript:\n%s", err, out, js)
	}
}
