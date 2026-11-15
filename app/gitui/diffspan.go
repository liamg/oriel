package gitui

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
