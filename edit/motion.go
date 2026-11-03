package edit

import (
	"fmt"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// Motions every engine can move by, by name: a character, a line, a word, the ends of the line and buffer.
// Columns count runes, and a move up or down aims for the column it started in.
const (
	CharNext  = "char-next"
	CharPrev  = "char-prev"
	LineNext  = "line-next"
	LinePrev  = "line-prev"
	WordNext  = "word-next" // to the end of this or the next word, as emacs's M-f
	WordPrev  = "word-prev" // to the start of this or the previous word
	LineStart = "line-start"
	LineEnd   = "line-end"
	BufStart  = "buffer-start"
	BufEnd    = "buffer-end"
)

// Motion is where the motion called name goes from off, count times; want is the column a line move aims for.
func Motion(s text.Snapshot, off int, name string, count int, want int) (int, error) {
	for range max(count, 1) {
		var err error
		if off, err = motion1(s, off, name, want); err != nil {
			return off, err
		}
	}
	return off, nil
}

func motion1(s text.Snapshot, off int, name string, want int) (int, error) {
	p := s.PosAt(off)
	line := s.Line(p.Line)
	switch name {
	case CharNext:
		if off >= s.Len() {
			return off, nil
		}
		_, n := utf8.DecodeRune(s.Slice(text.Range{Start: off, End: min(off+utf8.UTFMax, s.Len())}))
		return off + n, nil
	case CharPrev:
		if off == 0 {
			return 0, nil
		}
		_, n := utf8.DecodeLastRune(s.Slice(text.Range{Start: max(off-utf8.UTFMax, 0), End: off}))
		return off - n, nil
	case LineNext, LinePrev:
		target := p.Line + 1
		if name == LinePrev {
			target = p.Line - 1
		}
		if target < 0 {
			return 0, nil
		}
		if target >= s.Lines() {
			return s.Len(), nil
		}
		return s.LineStart(target) + byteCol(s.Line(target), want), nil
	case LineStart:
		return s.LineStart(p.Line), nil
	case LineEnd:
		return s.LineStart(p.Line) + len(line), nil
	case BufStart:
		return 0, nil
	case BufEnd:
		return s.Len(), nil
	case WordNext:
		rest := s.Slice(text.Range{Start: off, End: s.Len()})
		i := 0
		for i < len(rest) {
			r, n := utf8.DecodeRune(rest[i:])
			if isWord(r) {
				break
			}
			i += n
		}
		for i < len(rest) {
			r, n := utf8.DecodeRune(rest[i:])
			if !isWord(r) {
				break
			}
			i += n
		}
		return off + i, nil
	case WordPrev:
		before := s.Slice(text.Range{Start: 0, End: off})
		i := len(before)
		for i > 0 {
			r, n := utf8.DecodeLastRune(before[:i])
			if isWord(r) {
				break
			}
			i -= n
		}
		for i > 0 {
			r, n := utf8.DecodeLastRune(before[:i])
			if !isWord(r) {
				break
			}
			i -= n
		}
		return i, nil
	}
	return off, fmt.Errorf("no motion %q", name)
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// RuneCol is the column, in runes, of off on its line.
func RuneCol(s text.Snapshot, off int) int {
	p := s.PosAt(off)
	return utf8.RuneCount(s.Line(p.Line)[:min(p.Col, len(s.Line(p.Line)))])
}

// byteCol is the byte column of rune column want in line, or its end.
func byteCol(line []byte, want int) int {
	i := 0
	for n := 0; n < want && i < len(line); n++ {
		_, size := utf8.DecodeRune(line[i:])
		i += size
	}
	return i
}

// Replace puts text over the selection, or at the cursor, of every cursor, as one undo step (or part of g's),
// leaving each cursor after its text.
func (e *Editor) Replace(s string, g text.UndoGroup) error {
	cs := e.Cursors()
	snap := e.Buf()
	rs := make([]text.Replace, len(cs))
	for i, c := range cs {
		r, ok := c.Selection()
		if !ok {
			r = text.Range{Start: min(c.Off, snap.Len()), End: min(c.Off, snap.Len())}
		}
		rs[i] = text.Replace{Range: r, Text: []byte(s)}
	}
	order := make([]int, len(rs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return rs[a].Range.Start - rs[b].Range.Start })
	sorted := make([]text.Replace, len(rs))
	for k, i := range order {
		sorted[k] = rs[i]
	}
	if _, err := e.Do(sorted, g); err != nil {
		return err
	}
	out := make([]Cursor, len(cs))
	shift := 0
	for _, i := range order {
		r := rs[i].Range
		out[i] = Cursor{Off: r.Start + shift + len(s)}
		shift += len(s) - (r.End - r.Start)
	}
	e.SetCursors(out)
	return nil
}
