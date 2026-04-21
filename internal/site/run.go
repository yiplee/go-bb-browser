package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var reAnonAsyncHead = regexp.MustCompile(`(?s)\basync\s+function\s*\([^)]*\)\s*\{`)

// prepareAdapterJS fixes adapters written as `async function(args) { ... }`, which is a
// syntax error when nested inside RunScript's outer function (anonymous declaration).
// It becomes an async function expression assigned to __bb_run, then invoked.
func prepareAdapterJS(src []byte) []byte {
	s := string(src)
	s = stripExportDefaultAfterMeta(s)
	s = rewriteAnonymousAsyncAdapter(s)
	return []byte(s)
}

func stripExportDefaultAfterMeta(s string) string {
	// ESM default export is invalid inside our wrapper; some adapters use it.
	s = regexp.MustCompile(`(\*/\s*)export\s+default\s+`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`(?s)^\s*export\s+default\s+`).ReplaceAllString(s, "")
	return s
}

func rewriteAnonymousAsyncAdapter(s string) string {
	loc := reAnonAsyncHead.FindStringIndex(s)
	if loc == nil {
		return s
	}
	bodyOpen := loc[1] - 1
	if bodyOpen < 0 || s[bodyOpen] != '{' {
		return s
	}
	closeBrace := matchingJSBrace(s, bodyOpen)
	if closeBrace < 0 {
		return s
	}
	po := paramListOpenParen(s, loc[0], loc[1])
	if po < 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(s[:loc[0]])
	b.WriteString("const __bb_run = async function")
	b.WriteString(s[po:closeBrace])
	b.WriteString("}; return await __bb_run(__args);")
	b.WriteString(s[closeBrace+1:])
	return b.String()
}

// paramListOpenParen returns the index of '(' starting the formal parameter list
// for an "async function …" header spanning [hdrStart, hdrEnd).
func paramListOpenParen(s string, hdrStart, hdrEnd int) int {
	seg := s[hdrStart:hdrEnd]
	fi := strings.Index(seg, "function")
	if fi < 0 {
		return -1
	}
	j := hdrStart + fi + len("function")
	for j < hdrEnd && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j++
	}
	if j < hdrEnd && s[j] == '(' {
		return j
	}
	return -1
}

// matchingJSBrace returns the index of the `}` that matches the `{` at open, or -1.
// It skips //, /* */, and string literals (", ', `) with basic escape handling.
func matchingJSBrace(s string, open int) int {
	if open < 0 || open >= len(s) || s[open] != '{' {
		return -1
	}
	depth := 1
	i := open + 1
	for i < len(s) {
		// Line comment
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			continue
		}
		c := s[i]
		// String / template (no nested ${} — good enough for adapters)
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// RunScript wraps adapter source as an async IIFE for bb-browser run.
func RunScript(adapterSrc []byte, argsJSON string) string {
	a := strings.TrimSpace(argsJSON)
	if a == "" || a == "null" {
		a = "{}"
	}
	body := prepareAdapterJS(adapterSrc)
	var buf bytes.Buffer
	buf.WriteString("(async function(__args){\n")
	buf.Write(body)
	buf.WriteString("\n})( ")
	buf.WriteString(a)
	buf.WriteString(" )")
	return buf.String()
}

// ArgsObject builds a JSON object from positional args: first mapped to @meta
// "args" keys in source order from @meta, then always arg1, arg2, …, plus named flags.
func ArgsObject(positional []string, named map[string]string, metaArgKeys []string) ([]byte, error) {
	m := make(map[string]any)
	for i, p := range positional {
		if i < len(metaArgKeys) {
			k := strings.TrimSpace(metaArgKeys[i])
			if k != "" {
				m[k] = p
			}
		}
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
