package testrun

import (
	"bytes"
	"cmp"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// testMatches runs the "tests" query over src: through its document when it has one, else on a parser, borrowed for
// the call, of the language its path names in src.Languages.
func testMatches(src Source) []syntax.Match {
	if src.Syntax != nil {
		ms, _ := src.Syntax.Query("tests")
		return ms
	}
	langs := src.Languages
	if langs == nil {
		langs = syntax.Builtin()
	}
	l, ok := langs.ForFile(src.Path, firstLine(src.Text))
	if !ok {
		return nil
	}
	p, err := syntax.Borrow(l)
	if err != nil {
		return nil
	}
	defer p.Release()
	p.ParseSnapshot(src.Text)
	ms, _ := p.Query("tests")
	return ms
}

func firstLine(s text.Snapshot) []byte {
	b := s.Bytes()
	b = b[:min(len(b), 256)]
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[:i]
	}
	return b
}

// node is a capture with the match it came from, for working out what's inside what.
type node struct {
	syntax.Capture
	m syntax.Match
}

func (n node) contains(o node) bool {
	return n.Range.Start <= o.Range.Start && o.Range.End <= n.Range.End && n.Range != o.Range
}

// byCapture collects each capture called name across the matches, in source order.
func byCapture(ms []syntax.Match, name string) []node {
	var out []node
	seen := map[text.Range]bool{}
	for _, m := range ms {
		for _, c := range m.All(name) {
			if !seen[c.Range] {
				seen[c.Range] = true
				out = append(out, node{c, m})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b node) int { return cmp.Compare(a.Range.Start, b.Range.Start) })
	return out
}

// innermost is the smallest of ns around n.
func innermost(ns []node, n node) (node, bool) {
	var best node
	found := false
	for _, c := range ns {
		if c.contains(n) && (!found || best.contains(c)) {
			best, found = c, true
		}
	}
	return best, found
}

// relTo is path relative to dir, slash-separated.
func relTo(dir, path string) string {
	r, err := filepath.Rel(dir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

// findUp walks up from dir to the first directory holding one of names.
func findUp(dir string, names ...string) (string, bool) {
	for {
		for _, n := range names {
			if fileExists(filepath.Join(dir, n)) {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// sortItems puts items in line order, keeping the order of those on one line.
func sortItems(items []Item) {
	slices.SortStableFunc(items, func(a, b Item) int { return cmp.Compare(a.Line, b.Line) })
}
