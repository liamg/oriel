package widget

import (
	"image/color"
	"math"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A ring is a countdown dial (ui.md 10, "18m" before the next meeting): a track all round, an arc in the tone from
// twelve o'clock clockwise to Frac of the way, and the label in the middle; it reads as the label and the share.
func TestRing(t *testing.T) {
	violet := color.NRGBA{R: 0xb9, G: 0x8a, B: 0xff, A: 0xff}
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("row", ui.Style{}, func(c *ui.Ctx) {
			id = Ring(c, "ring", RingOpts{Frac: .3, Label: "18m", Tone: violet, Size: 48, Name: "next meeting", Help: "test/ring"})
		})
	})
	h.frame()
	r := h.rect(id)
	if r.Dx() != 48 || r.Dy() != 48 {
		t.Errorf("the ring's rect: %v", r)
	}
	if !h.drew("18m") {
		t.Errorf("didn't draw its label: %q", h.texts())
	}
	if n := h.access(id); n == nil || n.Name != "next meeting 18m" {
		t.Errorf("its accessible name: %+v", n)
	}
	cx, cy := mid(r)
	var arc *render.Path
	tracks := 0
	for _, op := range h.list.Ops {
		if op.Kind != render.OpPath {
			continue
		}
		if op.Style.Stroke == violet {
			arc = op.Path
		} else if op.Style.Stroke != (color.NRGBA{}) {
			tracks++
		}
	}
	if arc == nil || tracks == 0 {
		t.Fatalf("no arc in the tone, or no track: arc %v, %d tracks", arc, tracks)
	}
	angle := func(p render.Point) float64 { // clockwise from twelve o'clock, in turns
		a := math.Atan2(float64(p.X-cx), float64(cy-p.Y)) / (2 * math.Pi)
		if a < 0 {
			a++
		}
		return a
	}
	first, last := arc.Segs[0].Pts[0], arc.Segs[len(arc.Segs)-1].Pts[0]
	if a := angle(first); a > .01 && a < .99 {
		t.Errorf("the arc starts at %.3f of a turn, not twelve o'clock", a)
	}
	if a := angle(last); math.Abs(a-.3) > .01 {
		t.Errorf("the arc ends at %.3f of a turn, want .3", a)
	}
}
