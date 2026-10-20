package diagram

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Fonts are what a diagram's text is set in.
type Fonts struct {
	Label typeset.FontSpec // node and participant labels
	Small typeset.FontSpec // edge labels, messages and notes
	Title typeset.FontSpec // subgraph titles and block kinds
}

// Box is a placed node, participant or subgraph.
type Box struct {
	ID    string
	Label string
	Shape Shape
	Rect  render.Rect
}

// Route is a placed edge or message: Points run from its From's border to its To's.
type Route struct {
	Edge               int // its index in the Flowchart's Edges, or the Sequence's Items
	From, To           int // indexes into Nodes, or Groups where FromGroup / ToGroup
	FromGroup          bool
	ToGroup            bool
	Points             []render.Point
	Back               bool        // drawn against the ranks, to break a cycle
	Loop               bool        // from a node to itself
	Label              render.Rect // its label's pill; empty without one
	Vertical           bool        // its curves leave and enter vertically (TB, BT); horizontally otherwise
	Line               LineStyle
	StartMark, EndMark Marker
}

// Truncated is text drawn cut short with "…", where it's drawn, and what it says in full.
type Truncated struct {
	Rect render.Rect
	Text string
}

// Layout is a diagram measured with a shaper and placed, in logical pixels from its top left. It draws at any size.
type Layout struct {
	Kind      string // "flowchart" or "sequence"
	Size      render.Point
	Nodes     []Box
	Edges     []Route
	Groups    []Box
	Truncated []Truncated // text too long to draw whole: a word wider than any box should be, or too many lines

	prims []prim
	sh    typeset.Shaper
}

// Layout measures and places d.
func (d *Diagram) Layout(sh typeset.Shaper, f Fonts) *Layout {
	m := &measure{sh: sh}
	var l *Layout
	if d.Seq != nil {
		l = layoutSequence(d.Seq, m, f)
	} else {
		l = layoutFlow(d.Flow, m, f)
	}
	l.Kind, l.sh = d.Kind(), sh
	return l
}

type measure struct{ sh typeset.Shaper }

// block is text set in lines: its size and each line's baseline from its top.
type block struct {
	lines    []string
	font     typeset.FontSpec
	w, h     float32
	ascent   float32
	advances []float32
	lh       float32
	full     string // the text, when lines cut it short
}

// Text wraps rather than cuts: a line breaks at maxW, a word may run to twice that, and a label to maxLines lines.
const maxLines = 6

func (m *measure) width(s string, f typeset.FontSpec) float32 { return m.sh.Shape(s, f).Advance }

// text sets s in f, broken at its newlines and wrapped at maxW. A word too long for twice maxW, or a line past
// maxLines, is cut with "…", and the block keeps s in full.
func (m *measure) text(s string, f typeset.FontSpec, maxW float32) block {
	b := block{font: f}
	probe := m.sh.Shape("Hg", f)
	b.ascent = probe.Ascent
	b.lh = (probe.Ascent + probe.Descent) * 1.22
	if s == "" {
		return b
	}
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			b.lines = append(b.lines, "")
			continue
		}
		for i, w := range words {
			if m.width(w, f) > 2*maxW {
				words[i], b.full = typeset.Elide(m.sh, w, f, 2*maxW), s
			}
		}
		cur := words[0]
		for _, w := range words[1:] {
			if m.width(cur+" "+w, f) > maxW {
				b.lines = append(b.lines, cur)
				cur = w
			} else {
				cur += " " + w
			}
		}
		b.lines = append(b.lines, cur)
	}
	if len(b.lines) > maxLines {
		b.lines = append(b.lines[:maxLines-1], typeset.Elide(m.sh, strings.Join(b.lines[maxLines-1:], " "), f, maxW))
		b.full = s
	}
	for _, l := range b.lines {
		a := m.width(l, f)
		b.advances = append(b.advances, a)
		b.w = max(b.w, a)
	}
	b.h = b.lh*float32(len(b.lines)-1) + probe.Ascent + probe.Descent
	return b
}

// cut records where a block cut short is drawn, r, with its full text.
func (l *Layout) cut(b block, r render.Rect) {
	if b.full != "" {
		l.Truncated = append(l.Truncated, Truncated{Rect: r, Text: strings.ReplaceAll(b.full, "\n", " ")})
	}
}

// at adds the block's lines centred on (cx, cy).
func (b block) at(cx, cy float32, c role) []prim {
	var out []prim
	top := cy - b.h/2
	for i, l := range b.lines {
		out = append(out, prim{kind: primText, text: l, font: b.font, at: render.Point{X: cx, Y: top + b.ascent + b.lh*float32(i)}, fill: c, centre: true})
	}
	return out
}

// left adds the block's lines from (x, top).
func (b block) left(x, top float32, c role) []prim {
	var out []prim
	for i, l := range b.lines {
		out = append(out, prim{kind: primText, text: l, font: b.font, at: render.Point{X: x, Y: top + b.ascent + b.lh*float32(i)}, fill: c})
	}
	return out
}
