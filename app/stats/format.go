package stats

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// pct is f, a fraction, as a whole percentage.
func pct(f float64) int { return int(f*100 + .5) }

// hudDot is the heads-up display's small round status dot, in tone.
func hudDot(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: tone}, nil)
}
