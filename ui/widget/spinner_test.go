package widget

import (
	"image/color"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// A spinner is a quarter arc in its tone over a track; it goes round as time passes and asks for the frames that
// move it, holds still with reduced motion, and reads as what's in progress.
func TestSpinner(t *testing.T) {
	tone := color.NRGBA{R: 0x4f, G: 0xe0, B: 0xa0, A: 0xff}
	var id ui.ID
	build := func(c *ui.Ctx) {
		c.Row("row", ui.Style{}, func(c *ui.Ctx) {
			id = Spinner(c, "spin", SpinnerOpts{Size: 16, Tone: tone, Name: "starting the agent", Help: "test/spinner"})
		})
	}
	start := func(h *harness) render.Point {
		for _, op := range h.list.Ops {
			if op.Kind == render.OpPath && op.Style.Stroke == tone {
				return op.Path.Segs[0].Pts[0]
			}
		}
		t.Fatal("no arc in the tone")
		return render.Point{}
	}

	h := motionHarness(t, false, build)
	h.frame()
	if r := h.rect(id); r.Dx() != 16 || r.Dy() != 16 {
		t.Errorf("the spinner's rect: %v", r)
	}
	if n := h.access(id); n == nil || n.Name != "starting the agent" {
		t.Errorf("its accessible name: %+v", n)
	}
	before := start(h)
	if next := h.u.NextFrame(); next.IsZero() || next.After(h.now.Add(100*time.Millisecond)) {
		t.Errorf("a turning spinner asked for its next frame at %v (now %v)", next, h.now)
	}
	h.now = h.now.Add(spinnerTurn / 4)
	h.frame()
	if start(h) == before {
		t.Error("the arc didn't move as time passed")
	}

	still := motionHarness(t, true, build)
	still.frame()
	before = start(still)
	still.now = still.now.Add(spinnerTurn / 4)
	still.frame()
	if start(still) != before {
		t.Error("with reduced motion the arc moved")
	}
}
