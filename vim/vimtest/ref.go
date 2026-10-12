package vimtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// RequireRef fails the test unless the case cites a source: `file.txt *tag*` naming a tag in
// spec/vim (or `nvim/file.txt *tag*` for spec/nvim), `testdir/test_x.vim#Test_name` naming a
// function in vim's vendored tests, or `oracle nvim vX.Y.Z`. A quote, when given, must appear in
// the cited section or function, ignoring differences in whitespace.
func (c Case) RequireRef(t testing.TB) {
	t.Helper()
	if err := c.CheckRef(); err != nil {
		t.Fatalf("%s:%d: %v", c.File, c.Line, err)
	}
}

// CheckRef is RequireRef's check, as an error.
func (c Case) CheckRef() error {
	ref := strings.TrimSpace(c.Ref)
	if ref == "" {
		return errors.New("no ref: every case cites a source (see _design/testing.md)")
	}
	if rest, ok := strings.CutPrefix(ref, "oracle"); ok {
		if !strings.HasPrefix(strings.TrimSpace(rest), "nvim v") {
			return fmt.Errorf("ref %q: an oracle ref names the nvim version, e.g. `oracle nvim v0.11.7`", ref)
		}
		return nil
	}
	if file, fn, ok := strings.Cut(ref, "#"); ok && strings.HasSuffix(file, ".vim") {
		return c.checkTestFunc(ref, file, fn)
	}
	file, tag, ok := strings.Cut(ref, " ")
	tag = strings.TrimSpace(tag)
	if !ok || len(tag) < 3 || tag[0] != '*' || tag[len(tag)-1] != '*' {
		return fmt.Errorf("ref %q: want `file.txt *tag*`", ref)
	}
	text, err := specFile(file)
	if err != nil {
		return fmt.Errorf("ref %q: %w", ref, err)
	}
	at := tagAt(text, tag)
	if at < 0 {
		return fmt.Errorf("ref %q: no %s in %s", ref, tag, file)
	}
	if c.Quote != "" && !strings.Contains(squash(section(text, at)), squash(c.Quote)) {
		return fmt.Errorf("quote %q is not in the %s section of %s", c.Quote, tag, file)
	}
	return nil
}

var (
	vimFuncRe = regexp.MustCompile(`(?m)^fu(?:n|nc|nct|ncti|nctio|nction)?!?\s+([\w:#]+)\s*\(`)
	endfuncRe = regexp.MustCompile(`(?m)^endf`)
)

// checkTestFunc checks a `testdir/test_x.vim#Test_name` ref: the function is defined in the file.
func (c Case) checkTestFunc(ref, file, fn string) error {
	if !strings.HasPrefix(file, "testdir/") {
		return fmt.Errorf("ref %q: vim test refs are `testdir/test_x.vim#Test_name`", ref)
	}
	text, err := specFile(file)
	if err != nil {
		return fmt.Errorf("ref %q: %w", ref, err)
	}
	for _, m := range vimFuncRe.FindAllStringSubmatchIndex(text, -1) {
		if text[m[2]:m[3]] != fn {
			continue
		}
		body := text[m[0]:]
		if end := endfuncRe.FindStringIndex(body); end != nil {
			body = body[:end[1]]
		}
		if c.Quote != "" && !strings.Contains(squash(body), squash(c.Quote)) {
			return fmt.Errorf("quote %q is not in %s in %s", c.Quote, fn, file)
		}
		return nil
	}
	return fmt.Errorf("ref %q: no function %s in %s", ref, fn, file)
}

// tagAt returns the offset of the line defining tag (a `*tag*` delimited by whitespace), or -1.
func tagAt(text, tag string) int {
	for i := 0; ; {
		j := strings.Index(text[i:], tag)
		if j < 0 {
			return -1
		}
		start, end := i+j, i+j+len(tag)
		if (start == 0 || isSpace(text[start-1])) && (end == len(text) || isSpace(text[end])) {
			return strings.LastIndexByte(text[:start], '\n') + 1
		}
		i = start + 1
	}
}

// section is the help text from the line at offset at up to the next line defining a tag
// that follows some text.
func section(text string, at int) string {
	lines := strings.SplitAfter(text[at:], "\n")
	end, seenText := len(lines), false
	for i, l := range lines {
		if i > 0 && seenText && definesTag(l) {
			end = i
			break
		}
		if !onlyTags(l) {
			seenText = true
		}
	}
	return strings.Join(lines[:end], "")
}

func definesTag(line string) bool {
	for _, f := range strings.Fields(line) {
		if len(f) > 2 && f[0] == '*' && f[len(f)-1] == '*' {
			return true
		}
	}
	return false
}

func onlyTags(line string) bool {
	for _, f := range strings.Fields(line) {
		if len(f) < 3 || f[0] != '*' || f[len(f)-1] != '*' {
			return false
		}
	}
	return true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// squash collapses every run of whitespace to one space.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

var specCache sync.Map

// specFile reads a vendored help file: `name.txt` from spec/vim, `nvim/name.txt` from spec/nvim.
func specFile(name string) (string, error) {
	key := name
	if v, ok := specCache.Load(key); ok {
		return v.(string), nil
	}
	if strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", fmt.Errorf("bad spec path %q", name)
	}
	if !strings.HasPrefix(name, "nvim/") {
		name = "vim/" + name
	}
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(root, "spec", filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	specCache.Store(key, string(b))
	return string(b), nil
}

// RepoRoot walks up from the working directory to the directory holding go.work.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.work above the working directory")
		}
		dir = parent
	}
}
