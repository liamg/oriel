package kit

import (
	"fmt"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// Place is a point or span in a buffer as people and agents name it: 1-based lines, and 1-based characters (0 for
// whole lines). EndLine is 0 on one line; EndCol is 0 for a point.
type Place struct {
	Line, EndLine, Col, EndCol int
}

// PlaceAt is the place of the character at p.
func PlaceAt(snap text.Snapshot, p text.Pos) Place {
	return Place{Line: p.Line + 1, Col: charCol(snap, p)}
}

// PlaceOf is the place of r: its first and last characters, or its lines when it covers them whole.
func PlaceOf(snap text.Snapshot, r text.Range) Place {
	a, z := snap.PosAt(r.Start), snap.PosAt(max(r.End-1, r.Start))
	s := Place{Line: a.Line + 1}
	if z.Line > a.Line {
		s.EndLine = z.Line + 1
	}
	if r.Start == snap.LineStart(a.Line) && r.End >= snap.LineEnd(z.Line) {
		return s
	}
	s.Col, s.EndCol = charCol(snap, a), charCol(snap, z)
	return s
}

// String is "4", "4-6", "4:7" or "4:7-6:2".
func (s Place) String() string {
	end := max(s.EndLine, s.Line)
	switch {
	case s.Col > 0 && s.EndCol > 0:
		return fmt.Sprintf("%d:%d-%d:%d", s.Line, s.Col, end, s.EndCol)
	case s.Col > 0:
		return fmt.Sprintf("%d:%d", s.Line, s.Col)
	case s.EndLine > s.Line:
		return fmt.Sprintf("%d-%d", s.Line, s.EndLine)
	}
	return fmt.Sprint(s.Line)
}

func charCol(snap text.Snapshot, p text.Pos) int {
	line := snap.Line(p.Line)
	return utf8.RuneCount(line[:min(p.Col, len(line))]) + 1
}
