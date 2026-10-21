package widget

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tRingTrack = theme.DeclareOn("ring.track", "alpha(fg, 12%)", "bg.surface")
	tRingArc   = theme.Declare("ring.arc", "accent")
	tRingLabel = theme.Declare("ring.label", "fg")
)

type RingOpts struct {
	Frac  float64     // how much of the ring is lit, clockwise from twelve o'clock: 0–1
	Label string      // in the middle: "18m"
	Tone  color.NRGBA // the arc's colour; ring.arc when zero
	Size  float32     // across; 44 when 0
	Width float32     // the ring's stroke; 3 when 0
	Name  string      // what it measures, for assistive technology: "next meeting"
	Help  string
}

// Ring is a dial: a track all round and an arc over Frac of it, with a short label in the middle. The HUD's next
// meeting counts down on one.
func Ring(c *ui.Ctx, key any, o RingOpts) ui.ID {
	size, w := o.Size, o.Width
	if size == 0 {
		size = 44
	}
	if w == 0 {
		w = 3
	}
	frac := float64(c.Ease(easedKey{key}, float32(min(max(o.Frac, 0), 1))))
	tone := o.Tone
	if tone == (color.NRGBA{}) {
		tone = col(c, tRingArc)
	}
	track := col(c, tRingTrack)
	label := strings.TrimSpace(o.Name + " " + o.Label)
	return c.Box(key, ui.Style{W: ui.Px(size), H: ui.Px(size), Main: ui.Center, Cross: ui.Center, Role: ui.RoleStatus, Label: label, Help: o.Help, Keep: true},
		func(c *ui.Ctx) {
			c.Box("dial", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Paint("arc", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
					in := render.Rect{Min: render.Point{X: r.Min.X + w/2, Y: r.Min.Y + w/2}, Max: render.Point{X: r.Max.X - w/2, Y: r.Max.Y - w/2}}
					f.Path(render.Ellipse(in), render.Style{Stroke: track, Width: w})
					if frac > 0 {
						f.Path(arc(in, frac), render.Style{Stroke: tone, Width: w})
					}
				})
			})
			if o.Label != "" {
				c.Text("label", o.Label, monoText(size*.26, typeset.SemiBold, col(c, tRingLabel), 0))
			}
		})
}

// arc is the part of the ellipse in r from twelve o'clock clockwise through frac of a turn.
func arc(r render.Rect, frac float64) *render.Path { return arcFrom(r, 0, frac) }
