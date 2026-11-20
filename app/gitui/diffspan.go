package gitui

import "path/filepath"

// diffSpan is a range of lines a Shift-click picked: from at, the line selected then, to another line of its hunk.
// It holds only while at is still the selection.
type diffSpan struct {
	at diffSel
	to int
}

// spanned is the range of lines in sel's hunk Shift-click picked, first and last; ok is false with none.
func (st *diffState) spanned() (first, last int, ok bool) {
	if st.span.at != st.sel || st.sel.line < 0 {
		return 0, 0, false
	}
	return min(st.sel.line, st.span.to), max(st.sel.line, st.span.to), true
}

// picked reports whether s is selected: the line selected, or one of a range of lines Shift-click picked.
func (st *diffState) picked(s diffSel) bool {
	first, last, ok := st.spanned()
	if !ok || s.file != st.sel.file || s.hunk != st.sel.hunk {
		return s == st.sel
	}
	return s.line >= first && s.line <= last
}

// selLine is the new side's line (1-based) of the selection and the last line of its range: the line selected, or
// the hunk's changed lines.
func (st *diffState) selLine() (path string, line, end int, ok bool) {
	h, found := st.hunkAt(st.sel)
	if !found {
		return "", 0, 0, false
	}
	path = filepath.Join(st.root, filepath.FromSlash(st.sel.file))
	near := func(i int) int {
		for k := i; k >= 0; k-- {
			if n := h.Lines[k].New; n > 0 {
				return n
			}
		}
		return max(h.NewStart, 1)
	}
	if first, last, ok := st.spanned(); ok && last < len(h.Lines) {
		if a, b := near(first), near(last); b > a {
			return path, a, b, true
		}
	}
	if l := st.sel.line; l >= 0 && l < len(h.Lines) {
		return path, near(l), 0, true
	}
	first, last := 0, 0
	for i, l := range h.Lines {
		if l.Op == ' ' {
			continue
		}
		n := near(i)
		if first == 0 {
			first = n
		}
		last = n
	}
	if last == first {
		last = 0
	}
	return path, max(first, 1), last, true
}
