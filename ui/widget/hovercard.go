package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

type HoverCardOpts struct {
	Label string // its accessible name
	Help  string
	Width float32 // 0: as wide as its body
}

type hoverCardState struct{ shown bool }

// hoverSlack is how far outside the card the pointer can stray before it goes.
const hoverSlack = 8

// HoverCard shows body in a card beside anchor while the pointer is on the anchor, and keeps it while the pointer
// moves into the card to read it; it goes when the pointer leaves both. It never takes focus or keys. It reports
// whether it's showing.
func HoverCard(c *ui.Ctx, key any, anchor ui.ID, o HoverCardOpts, body func(c *ui.Ctx)) bool {
	id := c.Next(key)
	st := ui.State[hoverCardState](c, id)
	in := c.Interaction(anchor)
	over := in.Hovered || in.Inside
	if st.shown && !over {
		over = c.Interaction(id).Inside || hoverBridge(c, anchor, id, in.Pointer)
	}
	st.shown = over
	if !over {
		return false
	}
	w := ui.Size{}
	if o.Width > 0 {
		w = ui.Px(o.Width)
	}
	c.Column(key, ui.Style{W: w, Pad: ui.Pad(12), Gap: 8, Fill: col(c, tMenuBg), Stroke: col(c, tMenuBorder), Border: 1, Radius: 8, Z: ui.LayerPopup,
		Interactive: true, Float: &ui.Float{To: anchor, At: render.Point{X: 1}, Offset: render.Point{X: 4, Y: -6}}, Role: ui.RoleGroup,
		Label: o.Label, Help: o.Help}, body)
	return true
}

// hoverBridge reports whether p is on the card (with some slack), or between the anchor and the card.
func hoverBridge(c *ui.Ctx, anchor, card ui.ID, p render.Point) bool {
	a, okA := c.Rect(anchor)
	r, okC := c.Rect(card)
	if !okA || !okC {
		return false
	}
	near := render.Rect{Min: render.Point{X: r.Min.X - hoverSlack, Y: r.Min.Y - hoverSlack}, Max: render.Point{X: r.Max.X + hoverSlack, Y: r.Max.Y + hoverSlack}}
	gap := render.Rect{Min: render.Point{X: a.Min.X, Y: a.Min.Y}, Max: render.Point{X: r.Min.X + 1, Y: a.Max.Y}}
	in := func(r render.Rect) bool { return p.X >= r.Min.X && p.X <= r.Max.X && p.Y >= r.Min.Y && p.Y <= r.Max.Y }
	return in(near) || in(gap)
}
