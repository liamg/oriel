//go:build showcase

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func TestShowcaseImages(t *testing.T) {
	s := readmeScene(t)
	s.shot(t, "images-markdown")
	s.click(centre(s.accessRole(t, "image", "The editor and its threads").Bounds))
	s.frames(2)
	s.shot(t, "images-lightbox")
	p := render.Point{X: s.size.X * .6, Y: s.size.Y * .45}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: p, Scroll: render.Point{Y: -300}})
	s.frames(2)
	s.shot(t, "images-lightbox-zoomed")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hero.png"), pngBytes(t, photo(900, 560)), 0o644)
	os.WriteFile(filepath.Join(dir, "demo.mp4"), make([]byte, 3<<20), 0o644)
	f := newScene(t, "", dir, mockupSize)
	f.run("editor.open", registry.Args{"path": "hero.png"})
	f.frames(3)
	f.shot(t, "images-file-tab")
	f.run("editor.open", registry.Args{"path": "demo.mp4"})
	f.frames(3)
	f.shot(t, "images-media-card")

	d, _ := imageDiffScene(t, "logo.png")
	d.shot(t, "images-diff-side-by-side")
	d.click(d.textPoint(t, "Slider"))
	d.frames(2)
	r := d.accessRole(t, "slider", "logo.png").Bounds
	d.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}})
	d.frame()
	d.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}, Buttons: render.ButtonPrimary})
	d.frame()
	d.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: r.Min.X + r.Dx()*.35, Y: r.Min.Y + 30}, Buttons: render.ButtonPrimary})
	d.frames(2)
	d.shot(t, "images-diff-slider")
}
