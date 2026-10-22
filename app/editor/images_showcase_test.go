//go:build showcase

package editor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func TestShowcaseImages(t *testing.T) {
	s := readmeScene(t, nil)
	s.Shot("images-markdown")
	s.Click(apptest.Centre(s.NodeRole("image", "The editor and its threads").Bounds), 0)
	s.Frames(2)
	s.Shot("images-lightbox")
	p := render.Point{X: s.Size().X * .6, Y: s.Size().Y * .45}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: p, Scroll: render.Point{Y: -300}})
	s.Frames(2)
	s.Shot("images-lightbox-zoomed")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hero.png"), editor.PNGBytes(t, photo(900, 560)), 0o644)
	os.WriteFile(filepath.Join(dir, "demo.mp4"), make([]byte, 3<<20), 0o644)
	f := apptest.New(t, apptest.Options{WorkDir: dir, Size: apptest.MockupSize})
	f.Run("editor.open", registry.Args{"path": "hero.png"})
	f.Frames(3)
	f.Shot("images-file-tab")
	f.Run("editor.open", registry.Args{"path": "demo.mp4"})
	f.Frames(3)
	f.Shot("images-media-card")

}
