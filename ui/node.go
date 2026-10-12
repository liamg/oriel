package ui

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Node is a view tree, as built by registry views in any plugin tier.
type Node interface{ isNode() }

type TextNode struct{ Text string }

type ColumnNode struct{ Children []Node }

func (TextNode) isNode()   {}
func (ColumnNode) isNode() {}

func Text(s string) Node { return TextNode{Text: s} }

func Column(children ...Node) Node { return ColumnNode{Children: children} }

// Style is what Draw paints text with until theme tokens exist.
type Style struct {
	Font typeset.FontSpec
	FG   render.Color
}

// Draw paints n with its top-left at at, and returns the height it took.
func Draw(f render.Frame, sh typeset.Shaper, st Style, n Node, at render.Point) float32 {
	switch n := n.(type) {
	case TextNode:
		run := sh.Shape(n.Text, st.Font)
		f.Glyphs(run, render.Point{X: at.X, Y: at.Y + run.Ascent}, st.FG)
		return run.Ascent + run.Descent
	case ColumnNode:
		var h float32
		for _, c := range n.Children {
			h += Draw(f, sh, st, c, render.Point{X: at.X, Y: at.Y + h})
		}
		return h
	}
	return 0
}
