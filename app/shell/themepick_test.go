package shell_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// Pick mode over the editor: code reads back as its syntax group.
func TestThemePickSyntax(t *testing.T) {
	s := apptest.EditorScene(t)
	probeTh := theme.Probe(s.UI().Theme()) // what pick mode draws the window in, to read back
	probe := s.Host().Shot(probeTh)
	s.Run("Theme", registry.Args{"args": "pick"})
	var at render.Point
	found := false
	for y := probe.Rect.Min.Y; y < probe.Rect.Max.Y && !found; y++ {
		for x := probe.Rect.Min.X; x < probe.Rect.Max.X; x++ {
			c := probe.RGBAAt(x, y)
			if name, ok := probeTh.Probed(render.Color{R: c.R, G: c.G, B: c.B, A: 255}); ok && name == "@keyword" {
				at, found = render.Point{X: float32(x) + .5, Y: float32(y) + .5}, true
				break
			}
		}
	}
	if !found {
		t.Fatal("no @keyword painted in the probe")
	}
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.Golden("theme-pick")
	if !strings.Contains(s.Text(), "@keyword") {
		t.Errorf("over a keyword:\n%s", s.Text())
	}
}
