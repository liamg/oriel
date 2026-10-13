package text

import (
	"fmt"
	"iter"
	"unicode/utf8"
)

// Snapshot is an immutable version of a buffer. It is a small value: copying it copies nothing, and
// any number of goroutines can read it without locks while the buffer moves on.
// Offsets and positions passed to it must be in range; methods panic otherwise, as slicing does.
type Snapshot struct {
	root    *node
	version uint64
}

func (s Snapshot) r() *node {
	if s.root == nil {
		return emptyLeaf
	}
	return s.root
}

// Version counts the changes since the buffer was created.
func (s Snapshot) Version() uint64 { return s.version }

func (s Snapshot) Len() int { return s.r().sum.bytes }

// Lines is the number of newlines plus one: a text ending in a newline has an empty last line.
func (s Snapshot) Lines() int { return s.r().sum.lines + 1 }

func (s Snapshot) Bytes() []byte { return s.Slice(Range{0, s.Len()}) }

func (s Snapshot) String() string { return string(s.Bytes()) }

func (s Snapshot) checkRange(r Range) {
	if r.Start < 0 || r.Start > r.End || r.End > s.Len() {
		panic(fmt.Sprintf("text: range %d:%d out of bounds [0:%d]", r.Start, r.End, s.Len()))
	}
}

func (s Snapshot) checkLine(n int) {
	if n < 0 || n >= s.Lines() {
		panic(fmt.Sprintf("text: line %d out of range [0:%d)", n, s.Lines()))
	}
}

// Slice returns a copy of the text in r.
func (s Snapshot) Slice(r Range) []byte {
	s.checkRange(r)
	out := make([]byte, 0, r.Len())
	s.r().walk(0, r.Start, r.End, func(off int, l *node) bool {
		out = append(out, l.text[max(r.Start, off)-off:min(r.End, off+len(l.text))-off]...)
		return true
	})
	return out
}

func (s Snapshot) piece(r Range) piece {
	var p piece
	s.r().walk(0, r.Start, r.End, func(off int, l *node) bool {
		p = cat(p, l.piece.slice(max(r.Start, off)-off, min(r.End, off+len(l.text))-off))
		return true
	})
	return p
}

// LineStart is the offset of line n's first byte.
func (s Snapshot) LineStart(n int) int {
	s.checkLine(n)
	if n == 0 {
		return 0
	}
	return s.r().lineStart(n)
}

// LineEnd is the offset of line n's newline, or of the end of the text on the last line.
func (s Snapshot) LineEnd(n int) int {
	if n == s.Lines()-1 {
		return s.Len()
	}
	return s.LineStart(n+1) - 1
}

// Line returns a copy of line n without its newline.
func (s Snapshot) Line(n int) []byte {
	return s.Slice(Range{s.LineStart(n), s.LineEnd(n)})
}

// PosAt returns the line and column of offset off.
func (s Snapshot) PosAt(off int) Pos {
	s.checkRange(Range{off, off})
	line := s.r().before(off).lines
	return Pos{Line: line, Col: off - s.LineStart(line)}
}

// Offset returns the offset of p.
func (s Snapshot) Offset(p Pos) int {
	off := s.LineStart(p.Line) + p.Col
	if p.Col < 0 || off > s.LineEnd(p.Line) {
		panic(fmt.Sprintf("text: column %d out of range on line %d", p.Col, p.Line))
	}
	return off
}

// UTF16 returns p's column in UTF-16 code units, as LSP counts them by default.
func (s Snapshot) UTF16(p Pos) int {
	return s.r().before(s.Offset(p)).utf16 - s.r().before(s.LineStart(p.Line)).utf16
}

// PosFromUTF16 is the inverse of UTF16. A column inside a surrogate pair gives the start of its rune,
// and one past the end of the line gives the end of the line.
func (s Snapshot) PosFromUTF16(line, col int) Pos {
	start := s.LineStart(line)
	off := s.r().offsetAtUTF16(s.r().before(start).utf16 + max(col, 0))
	return Pos{Line: line, Col: min(off, s.LineEnd(line)) - start}
}

// Boundary reports whether off falls between two runes. Every offset an edit or an anchor uses must.
// Invalid UTF-8 is one rune per byte.
func (s Snapshot) Boundary(off int) bool {
	if off < 0 || off > s.Len() {
		return false
	}
	w0 := max(0, off-3)
	return boundary(s.Slice(Range{w0, min(s.Len(), off+3)}), off-w0)
}

// Runes walks the runes after off (Forward) or before it (Backward), yielding each one's offset.
// Invalid UTF-8 comes out as utf8.RuneError, one byte at a time.
func (s Snapshot) Runes(off int, dir Dir) iter.Seq2[int, rune] {
	s.checkRange(Range{off, off})
	if dir == Backward {
		return func(yield func(int, rune) bool) {
			s.r().walkBack(0, off, func(loff int, l *node) bool {
				b := l.text[:min(len(l.text), off-loff)]
				for i := len(b); i > 0; {
					j := i - 1
					for j > 0 && j > i-4 && !boundary(b, j) {
						j--
					}
					r, _ := utf8.DecodeRune(b[j:i])
					if !yield(loff+j, r) {
						return false
					}
					i = j
				}
				return true
			})
		}
	}
	return func(yield func(int, rune) bool) {
		s.r().walk(0, off, s.Len(), func(loff int, l *node) bool {
			for i := max(off, loff) - loff; i < len(l.text); {
				r, n := utf8.DecodeRune(l.text[i:])
				if !yield(loff+i, r) {
					return false
				}
				i += n
			}
			return true
		})
	}
}

// Authors walks who wrote the text in r, as runs of one author each.
func (s Snapshot) Authors(r Range) iter.Seq[AuthorSpan] {
	s.checkRange(r)
	return func(yield func(AuthorSpan) bool) {
		var cur AuthorSpan
		ok := s.r().walk(0, r.Start, r.End, func(off int, l *node) bool {
			for _, rn := range l.runs {
				start, end := max(off, r.Start), min(off+rn.n, r.End)
				off += rn.n
				if start >= end {
					continue
				}
				if cur.Range.Len() > 0 && cur.Author == rn.a && cur.Range.End == start {
					cur.Range.End = end
					continue
				}
				if cur.Range.Len() > 0 && !yield(cur) {
					return false
				}
				cur = AuthorSpan{Range{start, end}, rn.a}
			}
			return true
		})
		if ok && cur.Range.Len() > 0 {
			yield(cur)
		}
	}
}
