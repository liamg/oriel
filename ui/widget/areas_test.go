package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A float against an area lies where the area is laid out that frame, the first included; against an area the frame
// doesn't have, where that area would be, empty.
func TestAreaFloat(t *testing.T) {
	framed := true
	var under, corner ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("shell", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			if framed {
				Area(c, AreaTop, "top", ui.Style{W: ui.Flex(1), H: ui.Px(30)}, nil)
			}
			Area(c, AreaMain, "main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, nil)
			Area(c, AreaBottom, "bottom", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
		})
		under = c.Box("under", ui.Style{W: ui.Px(10), H: ui.Px(10), Z: ui.LayerToast,
			Float: AreaFloat(c, AreaTop, render.Point{X: .5, Y: 1}, render.Point{X: .5}, render.Point{Y: 4})}, nil)
		corner = c.Box("corner", ui.Style{W: ui.Px(10), H: ui.Px(10), Z: ui.LayerToast,
			Float: AreaFloat(c, AreaMain, render.Point{X: 1, Y: 1}, render.Point{X: 1, Y: 1}, render.Point{X: -6, Y: -6})}, nil)
	})
	h.frame()
	if r := h.rect(under); r.Min != (render.Point{X: small.X/2 - 5, Y: 34}) {
		t.Errorf("on the first frame, the float under the top area is at %v", r)
	}
	if r := h.rect(corner); r.Max != (render.Point{X: small.X - 6, Y: small.Y - 20 - 6}) {
		t.Errorf("on the first frame, the float in the main area's corner is at %v", r)
	}
	framed = false
	h.frame()
	h.frame()
	if r := h.rect(under); r.Min != (render.Point{X: small.X/2 - 5, Y: 4}) {
		t.Errorf("with no top area, the float under it is at %v, want under the window's top edge", r)
	}
}
