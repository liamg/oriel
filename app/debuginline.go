package app

import (
	"strings"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
)

// InlineValuesOption turns the debugger's inline values on and off.
const InlineValuesOption = "debug.inline_values"

var (
	tDebugValue   = theme.Declare("debug.value", "accent")
	tDebugValueBg = theme.DeclareOn("debug.value.bg", "alpha(accent, 8%)", "bg.base")
)

// inlineLines is how far above the frame's line values are shown when its function can't be found.
const inlineLines = 30

// inlineValueWidth is the most of a value shown inline; the Variables panel has it whole.
const inlineValueWidth = 40

// inlineCache is the inline values worked out for one stop's frame and one version of a buffer.
type inlineCache struct {
	buf     *text.Buffer
	version uint64
	gen     int
	frame   int
	vars    int
	syntax  bool
	lines   map[int]string
}

// inlineValues are the selected frame's variables after the lines of its function that name them, each where it's
// first named, from the function's first line to the frame's (08-debug).
func (x *debugExt) inlineValues(ctx registry.DecorationCtx) []registry.Decoration {
	s := x.sess
	f, ok := s.top()
	if !ok || f.Path != ctx.Path || ctx.Buffer == nil || !x.inlineOn() {
		return nil
	}
	vals := x.frameValues(s)
	if len(vals) == 0 {
		return nil
	}
	snap := ctx.Buffer.Snapshot()
	hl := x.sh.editor.docs[ctx.Buffer]
	if hl != nil && hl.doc == nil {
		hl = nil
	}
	c := x.inline
	if c == nil || c.buf != ctx.Buffer || c.version != snap.Version() || c.gen != s.gen || c.frame != s.frame || c.vars != len(vals) ||
		c.syntax != (hl != nil) {
		c = &inlineCache{buf: ctx.Buffer, version: snap.Version(), gen: s.gen, frame: s.frame, vars: len(vals), syntax: hl != nil,
			lines: valueLines(hl, snap, f.Line, vals)}
		x.inline = c
	}
	var out []registry.Decoration
	for line, t := range c.lines {
		if line >= ctx.Top && line < ctx.Bottom {
			out = append(out, registry.Decoration{Kind: registry.VirtualText, Line: line, Text: t, Style: "debug.value", Bg: "debug.value.bg",
				Priority: 35})
		}
	}
	return out
}

func (x *debugExt) inlineOn() bool {
	v, _, err := x.h.Options().Get(InlineValuesOption, registry.At{})
	return err != nil || v != false
}

// frameValues are the selected frame's arguments and locals by name, the first scope's winning.
func (x *debugExt) frameValues(s *debugSession) map[string]string {
	out := map[string]string{}
	for _, sc := range s.scopes {
		if sc.Expensive || sc.Hint == "registers" || strings.EqualFold(sc.Name, "globals") {
			continue
		}
		for _, v := range s.vars[sc.Ref] {
			if _, seen := out[v.Name]; !seen && isIdent(v.Name) {
				out[v.Name] = v.Value
			}
		}
	}
	return out
}

// valueLines are "name = value" texts by line, for lines from the start of the function around line to line. hl
// is the buffer's syntax, nil when it has none (yet).
func valueLines(hl *highlighter, snap text.Snapshot, line int, vals map[string]string) map[int]string {
	if line >= snap.Lines() {
		return nil
	}
	first := max(0, line-inlineLines)
	if hl != nil {
		if start, ok := enclosingFunction(hl, snap, line); ok {
			first = start
		}
	}
	idents := lineIdents(hl, snap, first, line)
	shown := map[string]bool{}
	out := map[int]string{}
	for l := first; l <= line; l++ {
		var parts []string
		for _, name := range idents[l] {
			v, ok := vals[name]
			if !ok || shown[name] {
				continue
			}
			shown[name] = true
			parts = append(parts, name+" = "+truncate(strings.ReplaceAll(v, "\n", " "), inlineValueWidth))
		}
		if len(parts) > 0 {
			out[l] = strings.Join(parts, "  ")
		}
	}
	return out
}

// enclosingFunction is the first line of the innermost function, method or closure around line, from the
// language's units query.
func enclosingFunction(hl *highlighter, snap text.Snapshot, line int) (int, bool) {
	ms, err := hl.doc.Query("units")
	if err != nil {
		return 0, false
	}
	at := snap.LineStart(line)
	best, found := text.Range{}, false
	for _, m := range ms {
		for _, c := range m.Captures {
			switch c.Name {
			case "unit.function", "unit.method", "unit.closure":
			default:
				continue
			}
			if c.Range.Start <= at && at < c.Range.End && (!found || c.Range.Start >= best.Start) {
				best, found = c.Range, true
			}
		}
	}
	if !found {
		return 0, false
	}
	return snap.PosAt(best.Start).Line, true
}

// lineIdents are the variables each line of [first, last] names, in order: the spans highlighted as variables or
// parameters, or with no syntax, the identifiers not after a ".".
func lineIdents(hl *highlighter, snap text.Snapshot, first, last int) map[int][]string {
	out := map[int][]string{}
	if hl != nil {
		start, end := snap.LineStart(first), snap.LineEnd(last)
		if spans, err := hl.doc.Highlights(start, end); err == nil && len(spans) > 0 {
			for _, sp := range spans {
				switch strings.TrimPrefix(sp.Capture, "@") {
				case "variable", "variable.parameter":
				default:
					continue
				}
				if sp.Range.Start < start || sp.Range.End > end {
					continue
				}
				l := snap.PosAt(sp.Range.Start).Line
				out[l] = append(out[l], string(snap.Slice(sp.Range)))
			}
			return out
		}
	}
	for l := first; l <= last; l++ {
		b := snap.Line(l)
		for i := 0; i < len(b); {
			if !identStart(b[i]) {
				i++
				continue
			}
			j := i
			for j < len(b) && (identStart(b[j]) || b[j] >= '0' && b[j] <= '9') {
				j++
			}
			if i == 0 || b[i-1] != '.' {
				out[l] = append(out[l], string(b[i:j]))
			}
			i = j
		}
	}
	return out
}

func identStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isIdent reports whether a variable's name could be written in code: not delve's "~r0" or debugpy's "special
// variables".
func isIdent(s string) bool {
	if s == "" || !identStart(s[0]) {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !identStart(c) && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
