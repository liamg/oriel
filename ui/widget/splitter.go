package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tSplitterLine = theme.Declare("splitter.line", "line")
	tSplitterHot  = theme.Declare("splitter.hot", "accent")
)

// splitterGrab is how wide the area either side of the line that takes the pointer is, in all.
const splitterGrab = 7

// SplitState is a Splitter's width, held by its caller: Size is 0 until it's first dragged, so the Default holds.
type SplitState struct {
	Size  float32
	start float32 // Size when the drag began
}

// SplitterOpts are a Splitter's limits.
type SplitterOpts struct {
	Default  float32 // its width until dragged, and what a double click puts back
	Min, Max float32 // the narrowest and widest it's dragged to; a Max under the Min gives the Min
	Label    string  // its name to assistive technology: "Resize the list"
	Help     string
}

// Width is the box's width now: its Size, or the Default, held between Min and Max.
func (st *SplitState) Width(o SplitterOpts) float32 {
	w := st.Size
	if w == 0 {
		w = o.Default
	}
	if o.Max > 0 {
		w = min(w, o.Max)
	}
	return max(w, o.Min)
}

// Splitter is the line after a box in a row, dragged sideways to resize the box (st, sized with st.Width), lit while
// it's under the pointer; a double click puts it back to its Default. It reports whether the width changed.
func Splitter(c *ui.Ctx, key any, st *SplitState, o SplitterOpts) bool {
	th := c.Theme()
	was := st.Width(o)
	c.Box(key, ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(tSplitterLine)}, func(c *ui.Ctx) {
		in := c.Interaction(c.Next("grab"))
		switch {
		case in.DoubleClicked:
			st.Size, st.start = 0, 0
		case in.Held:
			if from, ok := in.PressedAt(); ok {
				if st.start == 0 {
					st.start = was
				}
				st.Size = st.start + in.Pointer().X - from.X
				st.Size = st.Width(o)
			}
		default:
			st.start = 0
		}
		line := render.Color{}
		if in.Hovered || in.Held {
			line = th.C(tSplitterHot)
		}
		c.Box("grab", ui.Style{W: ui.Px(splitterGrab), H: ui.Flex(1), Interactive: true, NoHover: true, Cursor: render.CursorResizeCol,
			Role: ui.RoleSlider, Label: o.Label, Help: o.Help, Z: ui.LayerContent + 1, Main: ui.Center,
			Float: &ui.Float{At: render.Point{X: .5}, Self: render.Point{X: .5}, Fill: true}}, func(c *ui.Ctx) {
			c.Box("line", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: line}, nil)
		})
	})
	return st.Width(o) != was
}
