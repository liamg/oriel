package widget

import (
	"image/color"
	"math"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var tDots = theme.Declare("dots.fg", "status.working")

// A wave crosses the dots once a dotsCycle in dotsSteps ticks: a frame a tick, not one a refresh.
const (
	dotsCycle = 1200 * time.Millisecond
	dotsSteps = 9
	dotsN     = 3
)

type DotsOpts struct {
	Size float32     // each dot across; 5 when 0
	Tone color.NRGBA // dots.fg when zero
	Name string      // what's going on, for assistive technology: "Claude is working"
	Help string
}

// Dots says someone is at work with no known end: three dots with a wave of brightness crossing them, a turn of
// typing dots. With reduced motion, or the window unfocused, they hold still.
func Dots(c *ui.Ctx, key any, o DotsOpts) ui.ID {
	size := o.Size
	if size == 0 {
		size = 5
	}
	gap := size * .8
	tone := o.Tone
	if tone == (color.NRGBA{}) {
		tone = col(c, tDots)
	}
	loop := c.Animate(dotsCycle, dotsSteps)
	w := size*dotsN + gap*(dotsN-1)
	return c.Box(key, ui.Style{W: ui.Px(w), H: ui.Px(size), Role: ui.RoleStatus, Label: o.Name, Help: o.Help, Keep: true}, func(c *ui.Ctx) {
		c.Paint("dots", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			phase := -1.0
			if loop.Moving() {
				phase = loop.Phase()
			}
			for i := range dotsN {
				x := r.Min.X + float32(i)*(size+gap)
				in := render.Rect{Min: render.Point{X: x, Y: r.Min.Y}, Max: render.Point{X: x + size, Y: r.Min.Y + size}}
				f.Path(render.Ellipse(in), render.Style{Fill: dotTone(tone, phase, i)})
			}
		})
	})
}

// dotTone is dot i's colour at phase of a cycle: brightest as the wave passes it, faint between; a phase below 0 is
// the still pattern.
func dotTone(tone color.NRGBA, phase float64, i int) color.NRGBA {
	lift := .6
	if phase >= 0 {
		lift = max(0, math.Cos(2*math.Pi*(phase-float64(i)/(2*dotsN))))
	}
	tone.A = uint8(float64(tone.A) * (.3 + .7*lift))
	return tone
}
