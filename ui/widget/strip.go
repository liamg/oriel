package widget

import (
	"cmp"
	"fmt"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tStripMoreBg = theme.Declare("strip.more.bg", "bg.raised")
	tStripMoreFg = theme.Declare("strip.more.fg", "fg.muted")
)

// StripOpts are a Strip's items and the one it keeps in view.
type StripOpts struct {
	Items  []any  // the keys body builds its items with, for counting those past each edge
	Active any    // the item brought into view when it changes; nil for none
	Help   string // the help id of the counts at its edges
}

// Strip is a row of items, tabs say, that scrolls sideways when they don't fit: by the wheel, up and down too, or a
// trackpad, with no scrollbar. An edge with items past it shows how many ("‹ 3", "2 ›"), and a click there brings the
// next of them into view. Active is brought into view when it changes; between changes the strip scrolls freely.
func Strip(c *ui.Ctx, key any, st ui.Style, o StripOpts, body func(c *ui.Ctx)) ui.ID {
	st.Dir, st.ScrollX, st.Sideways, st.Bars = ui.Row, true, true, ui.BarsHidden
	id := c.Box(key, st, body)
	if o.Active != nil {
		c.KeepInView(id, ui.Child(id, o.Active))
	}
	box, ok := c.Rect(id)
	if !ok {
		return id
	}
	var left, right []ui.ID
	for _, k := range o.Items {
		item := ui.Child(id, k)
		r, ok := c.Rect(item)
		switch {
		case !ok:
		case r.Min.X < box.Min.X-0.5:
			left = append(left, item)
		case r.Max.X > box.Max.X+0.5:
			right = append(right, item)
		}
	}
	help := cmp.Or(o.Help, st.Help)
	if len(left) > 0 {
		stripMore(c, id, "\x00left", fmt.Sprintf("‹ %d", len(left)), left[len(left)-1], false, help)
	}
	if len(right) > 0 {
		stripMore(c, id, "\x00right", fmt.Sprintf("%d ›", len(right)), right[0], true, help)
	}
	return id
}

// stripMore is an edge's count of the items past it: a click brings next into view.
func stripMore(c *ui.Ctx, strip ui.ID, key any, label string, next ui.ID, end bool, help string) {
	f := &ui.Float{To: strip}
	if end {
		f.At, f.Self = render.Point{X: 1}, render.Point{X: 1}
	}
	r, _ := c.Rect(strip)
	if Clickable(c, key, ui.Style{H: ui.Px(r.Dy()), Pad: ui.PadXY(7, 0), Cross: ui.Center, Fill: col(c, tStripMoreBg), Float: f, Z: ui.LayerContent,
		Label: label, Tooltip: "More past this edge", Help: help}, func(c *ui.Ctx) {
		c.Text("n", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: col(c, tStripMoreFg)})
	}).Clicked {
		c.ScrollIntoView(strip, next)
	}
}
