package widget

import (
	"image/color"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// kappa puts a cubic's control points on a quarter circle.
const kappa = 0.5523

// Joined paints the box it's called in as a tab joined to the surface under it: fill, with its top corners rounded by
// radius and, at its foot, a curve flaring out radius past each side, so the tab runs into a surface of the same fill
// below it, as a browser's tab runs into its toolbar. It's drawn under the box's content.
func Joined(c *ui.Ctx, key any, fill color.NRGBA, radius float32) {
	k := c.Scale()
	c.Box(key, ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1), Z: ui.LayerUnder}, func(c *ui.Ctx) {
		c.Paint("shape", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			f.Path(joinedPath(r, radius*k), render.Style{Fill: fill})
		})
	})
}

// joinedPath is r with its top corners rounded by rad and its foot flared out rad past each side.
func joinedPath(r render.Rect, rad float32) *render.Path {
	rad = min(rad, r.Dx()/2, r.Dy()/2)
	x0, x1, y0, y1 := r.Min.X, r.Max.X, r.Min.Y, r.Max.Y
	q := rad * kappa
	p := &render.Path{}
	p.MoveTo(render.Point{X: x0 - rad, Y: y1})
	// The left foot: a quarter turn up into the tab's side.
	p.CubeTo(render.Point{X: x0 - rad + q, Y: y1}, render.Point{X: x0, Y: y1 - rad + q}, render.Point{X: x0, Y: y1 - rad})
	p.LineTo(render.Point{X: x0, Y: y0 + rad})
	p.CubeTo(render.Point{X: x0, Y: y0 + rad - q}, render.Point{X: x0 + rad - q, Y: y0}, render.Point{X: x0 + rad, Y: y0})
	p.LineTo(render.Point{X: x1 - rad, Y: y0})
	p.CubeTo(render.Point{X: x1 - rad + q, Y: y0}, render.Point{X: x1, Y: y0 + rad - q}, render.Point{X: x1, Y: y0 + rad})
	p.LineTo(render.Point{X: x1, Y: y1 - rad})
	p.CubeTo(render.Point{X: x1, Y: y1 - rad + q}, render.Point{X: x1 + rad - q, Y: y1}, render.Point{X: x1 + rad, Y: y1})
	return p.Close()
}
