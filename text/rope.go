package text

import (
	"slices"
	"unicode/utf8"
)

// Node sizes. Variables so that tests can shrink them and build deep trees from small texts.
var (
	maxLeaf = 4096 // bytes; a leaf can run up to 3 over to avoid splitting a rune
	minLeaf = 1024
	maxKids = 16
)

func minKids() int { return max(2, maxKids/2) }

// run is n bytes by one author.
type run struct {
	n int
	a Author
}

// piece is text with its authors. Pieces held by nodes are never modified.
type piece struct {
	text []byte
	runs []run
}

func newPiece(b []byte, a Author) piece {
	if len(b) == 0 {
		return piece{}
	}
	return piece{text: b, runs: []run{{len(b), a}}}
}

// appendRun appends r to rs, which the caller owns, merging it into the last run when the author matches.
func appendRun(rs []run, r run) []run {
	if r.n == 0 {
		return rs
	}
	if n := len(rs); n > 0 && rs[n-1].a == r.a {
		rs[n-1].n += r.n
		return rs
	}
	return append(rs, r)
}

func (p piece) slice(i, j int) piece {
	var runs []run
	off := 0
	for _, r := range p.runs {
		if s, e := max(off, i), min(off+r.n, j); s < e {
			runs = appendRun(runs, run{e - s, r.a})
		}
		off += r.n
		if off >= j {
			break
		}
	}
	return piece{text: p.text[i:j:j], runs: runs}
}

func cat(ps ...piece) piece {
	n := 0
	for _, p := range ps {
		n += len(p.text)
	}
	out := piece{text: make([]byte, 0, n)}
	for _, p := range ps {
		out.text = append(out.text, p.text...)
		for _, r := range p.runs {
			out.runs = appendRun(out.runs, r)
		}
	}
	return out
}

type summary struct {
	bytes, lines, utf16 int
}

func (s summary) add(o summary) summary {
	return summary{s.bytes + o.bytes, s.lines + o.lines, s.utf16 + o.utf16}
}

// measure counts b's lines and UTF-16 units. An invalid byte counts as one unit, as U+FFFD does.
func measure(b []byte) summary {
	s := summary{bytes: len(b)}
	for i := 0; i < len(b); {
		if c := b[i]; c < utf8.RuneSelf {
			if c == '\n' {
				s.lines++
			}
			s.utf16++
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		s.utf16++
		if r >= 0x10000 {
			s.utf16++
		}
		i += n
	}
	return s
}

// straddle reports how far a valid UTF-8 sequence in w reaches back before i and on past it,
// or zeros when i falls between runes. w needs up to 3 bytes either side of i, or the ends of the text.
func straddle(w []byte, i int) (back, fwd int) {
	for s := i - 1; s >= 0 && s >= i-3; s-- {
		if utf8.RuneStart(w[s]) {
			if _, n := utf8.DecodeRune(w[s:]); s+n > i {
				return i - s, s + n - i
			}
			return 0, 0
		}
	}
	return 0, 0
}

func boundary(w []byte, i int) bool {
	back, _ := straddle(w, i)
	return back == 0
}

// node is a B-tree node. Every leaf sits at height 0, and leaves start and end between runes,
// so that each one's summary is exact on its own.
type node struct {
	sum    summary
	height int
	piece          // leaves
	kids   []*node // inner nodes
}

var emptyLeaf = &node{}

func newLeaf(p piece) *node { return &node{sum: measure(p.text), piece: p} }

func newInner(kids []*node) *node {
	n := &node{height: kids[0].height + 1, kids: kids}
	for _, k := range kids {
		n.sum = n.sum.add(k.sum)
	}
	return n
}

func (n *node) underfull() bool {
	if n.height == 0 {
		return len(n.text) < minLeaf
	}
	return len(n.kids) < minKids()
}

// chunk cuts p into leaves of about maxLeaf bytes, between runes.
func chunk(p piece) []*node {
	if len(p.text) == 0 {
		return nil
	}
	k := (len(p.text) + maxLeaf - 1) / maxLeaf
	out := make([]*node, 0, k)
	start := 0
	for i := 1; i <= k; i++ {
		end := len(p.text) * i / k
		for !boundary(p.text, end) {
			end--
		}
		if end > start {
			out = append(out, newLeaf(p.slice(start, end)))
		}
		start = end
	}
	return out
}

// group puts nodes of one height under as few parents as fit.
func group(kids []*node) []*node {
	k := (len(kids) + maxKids - 1) / maxKids
	out := make([]*node, 0, k)
	for i := range k {
		out = append(out, newInner(slices.Clone(kids[len(kids)*i/k:len(kids)*(i+1)/k])))
	}
	return out
}

// join merges two neighbours of one height into one node, or two balanced ones when they don't fit in one.
func join(a, b *node) []*node {
	if a.height == 0 {
		return chunk(cat(a.piece, b.piece))
	}
	return group(slices.Concat(a.kids, b.kids))
}

// rebalance merges underfull neighbours among kids[lo:hi].
func rebalance(kids []*node, lo, hi int) []*node {
	for i := lo; i+1 < hi && i+1 < len(kids); {
		a, b := kids[i], kids[i+1]
		if !a.underfull() && !b.underfull() {
			i++
			continue
		}
		merged := join(a, b)
		kids = slices.Replace(kids, i, i+2, merged...)
		hi += len(merged) - 2
		if len(merged) > 1 {
			i++
		}
	}
	return kids
}

// replace returns nodes of n's height holding n's text with [from, to) replaced by p.
// from and to fall between runes, and so does each end of p once it is in place.
func (n *node) replace(from, to int, p piece) []*node {
	if n.height == 0 {
		return chunk(cat(n.piece.slice(0, from), p, n.piece.slice(to, len(n.text))))
	}
	kids := make([]*node, 0, len(n.kids)+2)
	off, k := 0, 0
	for ; k < len(n.kids)-1; k++ {
		end := off + n.kids[k].sum.bytes
		if end >= from {
			break
		}
		kids = append(kids, n.kids[k])
		off = end
	}
	lo := max(0, len(kids)-1)
	c := n.kids[k]
	end := off + c.sum.bytes
	kids = append(kids, c.replace(from-off, min(to, end)-off, p)...)
	off = end
	for k++; k < len(n.kids) && off < to; k++ {
		c := n.kids[k]
		end := off + c.sum.bytes
		if end > to {
			kids = append(kids, c.replace(0, to-off, piece{})...)
		}
		off = end
	}
	hi := len(kids) + 1
	kids = append(kids, n.kids[k:]...)
	kids = rebalance(kids, lo, hi)
	if len(kids) == 0 {
		return nil
	}
	return group(kids)
}

func replaceRoot(root *node, from, to int, p piece) *node {
	nodes := root.replace(from, to, p)
	for len(nodes) > 1 {
		nodes = group(nodes)
	}
	if len(nodes) == 0 {
		return emptyLeaf
	}
	r := nodes[0]
	for r.height > 0 && len(r.kids) == 1 {
		r = r.kids[0]
	}
	return r
}

// walk calls fn for each leaf overlapping [from, to), with the leaf's offset, until fn returns false.
func (n *node) walk(off, from, to int, fn func(off int, leaf *node) bool) bool {
	if n.height == 0 {
		return fn(off, n)
	}
	for _, c := range n.kids {
		end := off + c.sum.bytes
		if end > from && off < to {
			if !c.walk(off, from, to, fn) {
				return false
			}
		}
		if end >= to {
			break
		}
		off = end
	}
	return true
}

// walkBack calls fn for each leaf overlapping [0, to), last first, until fn returns false.
func (n *node) walkBack(off, to int, fn func(off int, leaf *node) bool) bool {
	if n.height == 0 {
		return fn(off, n)
	}
	end := off + n.sum.bytes
	for _, c := range slices.Backward(n.kids) {
		start := end - c.sum.bytes
		if start < to {
			if !c.walkBack(start, to, fn) {
				return false
			}
		}
		end = start
	}
	return true
}

// lineStart returns the offset just after the line-th newline; line is at least 1.
func (n *node) lineStart(line int) int {
	off := 0
	for n.height > 0 {
		for _, c := range n.kids {
			if c.sum.lines >= line {
				n = c
				break
			}
			line -= c.sum.lines
			off += c.sum.bytes
		}
	}
	for i, b := range n.text {
		if b == '\n' {
			if line--; line == 0 {
				return off + i + 1
			}
		}
	}
	panic("text: line out of range")
}

// before sums the text in [0, off).
func (n *node) before(off int) summary {
	var s summary
	for n.height > 0 {
		for i, c := range n.kids {
			if off >= c.sum.bytes && i < len(n.kids)-1 {
				s = s.add(c.sum)
				off -= c.sum.bytes
				continue
			}
			n = c
			break
		}
	}
	return s.add(measure(n.text[:off]))
}

// offsetAtUTF16 returns the offset after u UTF-16 units, or the start of the rune that holds the u-th.
func (n *node) offsetAtUTF16(u int) int {
	off := 0
	for n.height > 0 {
		for i, c := range n.kids {
			if u >= c.sum.utf16 && i < len(n.kids)-1 {
				u -= c.sum.utf16
				off += c.sum.bytes
				continue
			}
			n = c
			break
		}
	}
	for i := 0; i < len(n.text); {
		r, size := utf8.DecodeRune(n.text[i:])
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if u < w {
			return off + i
		}
		u -= w
		i += size
	}
	return off + len(n.text)
}
