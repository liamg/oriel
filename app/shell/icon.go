package shell

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

var (
	tIconBg   = theme.Declare("icon.bg", "bg.surface")
	tIconEdge = theme.Declare("icon.edge", "line2")
)

// Icon draws the app icon into the square r: the logo on a rounded square, inset by the margin macOS app icons
// leave (824 of 1024, corner radius 185).
func Icon(f render.Frame, r render.Rect, th *theme.Resolved) {
	u := r.Dx() / 1024
	sq := render.XYWH(r.Min.X+100*u, r.Min.Y+100*u, 824*u, 824*u)
	f.Rect(sq, render.Style{Fill: th.C(tIconBg), Radius: 185 * u, Stroke: th.C(tIconEdge), Width: 6 * u})
	// The logo's ink is centred on (47, 53) of its 100-unit box.
	size := 660 * u
	s := size / 100
	cx, cy := sq.Min.X+sq.Dx()/2, sq.Min.Y+sq.Dy()/2
	kit.Logo(f, render.XYWH(cx-47*s, cy-53*s, size, size), th)
}
