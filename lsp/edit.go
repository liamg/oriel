package lsp

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/liamg/oriel/text"
)

// EditFor turns a server's edits to one document into a text.Edit on s, the snapshot they were computed on.
// Edits that start at the same place keep their order.
func EditFor(s text.Snapshot, edits []TextEdit, a text.Author) (text.Edit, error) {
	type off struct {
		r text.Replace
		i int
	}
	rs := make([]off, 0, len(edits))
	for i, e := range edits {
		start, err := offset(s, e.Range.Start)
		if err != nil {
			return text.Edit{}, err
		}
		end, err := offset(s, e.Range.End)
		if err != nil {
			return text.Edit{}, err
		}
		if end < start {
			return text.Edit{}, fmt.Errorf("lsp: edit %d ends before it starts", i)
		}
		rs = append(rs, off{text.Replace{Range: text.Range{Start: start, End: end}, Text: []byte(e.NewText)}, i})
	}
	slices.SortStableFunc(rs, func(a, b off) int { return cmp.Compare(a.r.Range.Start, b.r.Range.Start) })
	out := text.Edit{Author: a}
	for i, r := range rs {
		if i > 0 && r.r.Range.Start < rs[i-1].r.Range.End {
			return text.Edit{}, fmt.Errorf("lsp: edits %d and %d overlap", rs[i-1].i, r.i)
		}
		out.Ranges = append(out.Ranges, r.r)
	}
	return out, nil
}

func offset(s text.Snapshot, p text.Pos) (int, error) {
	if p.Line < 0 || p.Line >= s.Lines() || p.Col < 0 || p.Col > s.LineEnd(p.Line)-s.LineStart(p.Line) {
		return 0, fmt.Errorf("lsp: position %v is outside the document", p)
	}
	return s.Offset(p), nil
}
