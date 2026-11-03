package text

import (
	"bytes"
	"slices"
	"unicode/utf16"
	"unicode/utf8"
)

// model is the naive reference the rope and buffer are checked against: a []byte, one author per byte,
// anchors as plain offsets and an undo tree of whole copies.
type model struct {
	text    []byte
	authors []Author
	anchors map[AnchorID]modelAnchor
	head    *modelNode
	root    *modelNode
	seq     int
}

type modelAnchor struct {
	off  int
	bias Bias
}

type modelNode struct {
	seq    int
	parent *modelNode
	kids   []*modelNode
	redo   *modelNode
	author Author
	group  UndoGroup
	ops    []modelOp
}

// modelOp replaces del at at with ins.
type modelOp struct {
	at                     int
	del, ins               []byte
	delAuthors, insAuthors []Author
}

func newModel(content []byte, a Author) *model {
	m := &model{
		text:    slices.Clone(content),
		authors: slices.Repeat([]Author{a}, len(content)),
		anchors: map[AnchorID]modelAnchor{},
	}
	m.root = &modelNode{}
	m.head = m.root
	return m
}

// modelBoundary walks the whole text from the start: the definition the rope's local check must agree with.
// The walk is cached for the last text it was asked about.
func modelBoundary(b []byte, off int) bool {
	if off < 0 || off > len(b) {
		return false
	}
	return boundariesOf(b)[off]
}

var boundaryCache struct {
	text []byte
	at   []bool
}

func boundariesOf(b []byte) []bool {
	if bytes.Equal(boundaryCache.text, b) && boundaryCache.at != nil {
		return boundaryCache.at
	}
	at := make([]bool, len(b)+1)
	for i := 0; i < len(b); {
		at[i] = true
		_, n := utf8.DecodeRune(b[i:])
		i += n
	}
	at[len(b)] = true
	boundaryCache.text, boundaryCache.at = slices.Clone(b), at
	return at
}

// replace applies one replace, widened so that no valid UTF-8 sequence straddles either end of the
// inserted text, and maps the anchors through it. It returns the replace as applied.
func (m *model) replace(from, to int, ins []byte, insAuthors []Author) modelOp {
	newText := slices.Concat(m.text[:from], ins, m.text[to:])
	a, b := from, to
	if !modelBoundary(newText, from) {
		for !modelBoundary(newText, a) {
			a--
		}
	}
	if end := from + len(ins); !modelBoundary(newText, end) {
		e := end
		for !modelBoundary(newText, e) {
			e++
		}
		b = to + (e - end)
	}
	eff := slices.Concat(m.text[a:from], ins, m.text[to:b])
	effAuthors := slices.Concat(m.authors[a:from], insAuthors, m.authors[to:b])
	op := modelOp{at: a, del: slices.Clone(m.text[a:b]), delAuthors: slices.Clone(m.authors[a:b]), ins: eff, insAuthors: effAuthors}
	m.text = slices.Concat(m.text[:a], eff, m.text[b:])
	m.authors = slices.Concat(m.authors[:a], effAuthors, m.authors[b:])
	for id, an := range m.anchors {
		an.off = modelMap(an.off, an.bias, a, b, len(eff))
		m.anchors[id] = an
	}
	return op
}

func modelMap(off int, bias Bias, from, to, n int) int {
	switch {
	case off < from:
		return off
	case off > to:
		return off + n - (to - from)
	case from == to:
		if bias == Left {
			return from
		}
		return from + n
	case off == from:
		return from
	case off == to:
		return from + n
	case bias == Left:
		return from
	default:
		return from + n
	}
}

// apply plays an edit's replaces from last to first, as the buffer does.
func (m *model) apply(e Edit) {
	var ops []modelOp
	for i := len(e.Ranges) - 1; i >= 0; i-- {
		r := e.Ranges[i]
		ops = append(ops, m.replace(r.Range.Start, r.Range.End, r.Text, slices.Repeat([]Author{e.Author}, len(r.Text))))
	}
	if e.Group != 0 && m.head != m.root && m.head.group == e.Group && m.head.author == e.Author && len(m.head.kids) == 0 {
		m.head.ops = append(m.head.ops, ops...)
		return
	}
	m.seq++
	n := &modelNode{seq: m.seq, parent: m.head, author: e.Author, group: e.Group, ops: ops}
	m.head.kids = append(m.head.kids, n)
	m.head.redo = n
	m.head = n
}

func (m *model) undo() bool {
	if m.head == m.root {
		return false
	}
	for _, o := range slices.Backward(m.head.ops) {
		m.replace(o.at, o.at+len(o.ins), o.del, o.delAuthors)
	}
	m.head.parent.redo = m.head
	m.head = m.head.parent
	return true
}

func (m *model) redo() bool {
	n := m.head.redo
	if n == nil {
		return false
	}
	for _, o := range n.ops {
		m.replace(o.at, o.at+len(o.del), o.ins, o.insAuthors)
	}
	m.head = n
	return true
}

func (m *model) lines() int { return bytes.Count(m.text, []byte("\n")) + 1 }

func (m *model) lineStart(n int) int {
	off := 0
	for range n {
		off += bytes.IndexByte(m.text[off:], '\n') + 1
	}
	return off
}

func (m *model) line(n int) []byte {
	s := m.lineStart(n)
	if i := bytes.IndexByte(m.text[s:], '\n'); i >= 0 {
		return m.text[s : s+i]
	}
	return m.text[s:]
}

func (m *model) posAt(off int) Pos {
	l := bytes.Count(m.text[:off], []byte("\n"))
	return Pos{Line: l, Col: off - m.lineStart(l)}
}

func utf16Len(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		n += len(utf16.Encode([]rune{r}))
		b = b[size:]
	}
	return n
}

// runes decodes the text from the start, as (offset, rune) pairs.
func (m *model) runes() (offs []int, rs []rune) {
	for i := 0; i < len(m.text); {
		r, n := utf8.DecodeRune(m.text[i:])
		offs = append(offs, i)
		rs = append(rs, r)
		i += n
	}
	return offs, rs
}

// authorRuns merges the per-byte authors into runs.
func (m *model) authorRuns(from, to int) []AuthorSpan {
	var out []AuthorSpan
	for i := from; i < to; i++ {
		if len(out) > 0 && out[len(out)-1].Author == m.authors[i] {
			out[len(out)-1].Range.End++
			continue
		}
		out = append(out, AuthorSpan{Range{i, i + 1}, m.authors[i]})
	}
	return out
}
