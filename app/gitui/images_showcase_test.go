//go:build showcase

package gitui_test

import (
	"testing"

	"github.com/liamg/oriel/render"
)

func TestShowcaseImageDiff(t *testing.T) {
	d, _ := imageDiffScene(t, "logo.png")
	d.Shot("images-diff-side-by-side")
	d.Click(d.TextPoint("Slider"), 0)
	d.Frames(2)
	r := d.NodeRole("slider", "logo.png").Bounds
	d.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}})
	d.Frame()
	d.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}, Buttons: render.ButtonPrimary})
	d.Frame()
	d.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}, Buttons: render.ButtonPrimary})
	d.Frames(2)
	d.Shot("images-diff-slider")
}
