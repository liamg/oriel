package widget

import (
	"image/color"
	"slices"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// Dots are three dots in their tone; a wave crosses them as time passes, asking for the frames that move it, they
// hold still with reduced motion, and they read as what's going on.
func TestDots(t *testing.T) {
	tone := color.NRGBA{R: 0x4c, G: 0xc9, B: 0xf0, A: 0xff}
	var id ui.ID
	build := func(c *ui.Ctx) {
		c.Row("row", ui.Style{}, func(c *ui.Ctx) {
			id = Dots(c, "dots", DotsOpts{Size: 6, Tone: tone, Name: "Claude is working", Help: "test/dots"})
		})
	}
	alphas := func(h *harness) []uint8 {
		var as []uint8
		for _, op := range h.list.Ops {
			if f := op.Style.Fill; op.Kind == render.OpPath && f.R == tone.R && f.G == tone.G && f.B == tone.B && f.A > 0 {
				as = append(as, f.A)
			}
		}
		if len(as) != 3 {
			t.Fatalf("%d dots in the tone, not 3", len(as))
		}
		return as
	}

	h := motionHarness(t, false, build)
	h.frame()
	if r := h.rect(id); r.Dy() != 6 || r.Dx() <= 18 {
		t.Errorf("the dots' rect: %v", r)
	}
	if n := h.access(id); n == nil || n.Name != "Claude is working" || n.Role != ui.RoleStatus {
		t.Errorf("they read as %+v", n)
	}
	before := alphas(h)
	if slices.Equal(before, []uint8{before[0], before[0], before[0]}) {
		t.Errorf("the dots are all one brightness while moving: %v", before)
	}
	if next := h.u.NextFrame(); next.IsZero() || next.After(h.clock.Now().Add(dotsCycle/dotsSteps)) {
		t.Errorf("moving dots asked for their next frame at %v (now %v)", next, h.clock.Now())
	}
	h.clock.Advance(dotsCycle / 3)
	h.frame()
	if slices.Equal(alphas(h), before) {
		t.Error("the wave didn't move as time passed")
	}

	still := motionHarness(t, true, build)
	still.frame()
	before = alphas(still)
	still.clock.Advance(dotsCycle / 3)
	still.frame()
	if !slices.Equal(alphas(still), before) {
		t.Error("with reduced motion the dots moved")
	}
}
