package widget

import (
	"image/color"
	"math"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tSpinnerTrack = theme.DeclareOn("spinner.track", "alpha(fg, 10%)", "bg.surface")
	tSpinnerArc   = theme.Declare("spinner.arc", "accent")
)

// The spinner goes round once a spinnerTurn in spinnerSteps ticks: a frame a tick, not one a refresh.
const (
	spinnerTurn  = 1200 * time.Millisecond
	spinnerSteps = 12
)

type SpinnerOpts struct {
	Size float32     // across; 14 when 0
	Tone color.NRGBA // the arc's colour; spinner.arc when zero
	Name string      // what's in progress, for assistive technology: "starting the agent"
	Help string
}

// Spinner says something is in progress with no known end: a quarter arc going round a faint track. With reduced
// motion, or the window unfocused, it holds still.
func Spinner(c *ui.Ctx, key any, o SpinnerOpts) ui.ID {
	size := o.Size
	if size == 0 {
		size = 14
	}
	w := max(size/8, 1.5)
	tone := o.Tone
	if tone == (color.NRGBA{}) {
		tone = col(c, tSpinnerArc)
	}
	track := col(c, tSpinnerTrack)
	turn, _ := c.Decorative(spinnerTurn, spinnerSteps)
	return c.Box(key, ui.Style{W: ui.Px(size), H: ui.Px(size), Role: ui.RoleStatus, Label: o.Name, Help: o.Help, Keep: true}, func(c *ui.Ctx) {
		c.Paint("arc", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			in := render.Rect{Min: render.Point{X: r.Min.X + w/2, Y: r.Min.Y + w/2}, Max: render.Point{X: r.Max.X - w/2, Y: r.Max.Y - w/2}}
			f.Path(render.Ellipse(in), render.Style{Stroke: track, Width: w})
			f.Path(arcFrom(in, turn, .25), render.Style{Stroke: tone, Width: w})
		})
	})
}

// arcFrom is the part of the ellipse in r from start of a turn past twelve o'clock, clockwise through frac of one.
func arcFrom(r render.Rect, start, frac float64) *render.Path {
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	rx, ry := float64(r.Dx())/2, float64(r.Dy())/2
	n := max(int(frac*96), 2)
	p := new(render.Path)
	for i := 0; i <= n; i++ {
		a := 2 * math.Pi * (start + frac*float64(i)/float64(n))
		pt := render.Point{X: float32(cx + rx*math.Sin(a)), Y: float32(cy - ry*math.Cos(a))}
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	return p
}
