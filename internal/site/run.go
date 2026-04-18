package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RunScript wraps adapter source as an async IIFE like bb-browser site run.
func RunScript(adapterSrc []byte, argsJSON string) string {
	a := strings.TrimSpace(argsJSON)
	if a == "" || a == "null" {
		a = "{}"
	}
	var buf bytes.Buffer
	buf.WriteString("(async function(__args){\n")
	buf.Write(adapterSrc)
	buf.WriteString("\n})( ")
	buf.WriteString(a)
	buf.WriteString(" )")
	return buf.String()
}

// ArgsObject builds a JSON object from positional args (keys arg1, arg2, …) plus named pairs from remainder.
func ArgsObject(positional []string, named map[string]string) ([]byte, error) {
	m := make(map[string]any)
	for i, p := range positional {
		m[fmt.Sprintf("arg%d", i+1)] = p
	}
	for k, v := range named {
		k = strings.TrimPrefix(k, "--")
		if k != "" {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// WriteTempAdapter writes JS to a temp file for debugging (optional).
func WriteTempAdapter(js string) (path string, err error) {
	f, err := os.CreateTemp("", "bb-site-*.js")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(js); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
