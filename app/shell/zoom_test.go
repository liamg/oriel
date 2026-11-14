package shell_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// ui.font_size zooms the window, so every part grows together; ⌘= ⌘- ⌘0 step it.
func TestUIFontSizeZoomsTheWindow(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir()})
	before := r.Win.Size()
	waitZoom := func(what string, z float32) {
		t.Helper()
		want := render.Point{X: before.X / z, Y: before.Y / z}
		apptest.Poll(t, fmt.Sprintf("%s: scale %v, size %v", what, z, want), func() bool { return r.Win.Scale() == z && r.Win.Size() == want })
	}
	r.Do(func() error {
		return r.Host.Options().Set("ui.font_size", registry.LayerUser, registry.At{}, 19.5)
	})
	waitZoom("ui.font_size 19.5", 1.5)
	font := func() float64 {
		var v any
		r.Do(func() error { v, _, _ = r.Host.Options().Get("ui.font_size", registry.At{}); return nil })
		return v.(float64)
	}
	for _, step := range []struct {
		cmd  string
		want float64
	}{{"ui.zoom.in", 20.5}, {"ui.zoom.out", 19.5}, {"ui.zoom.out", 18.5}, {"ui.zoom.reset", 13}} {
		r.Do(func() error { return r.Host.Run(context.Background(), step.cmd, nil) })
		if got := font(); math.Abs(got-step.want) > 1e-9 {
			t.Errorf("after %s: ui.font_size %v, want %v", step.cmd, got, step.want)
		}
	}
	waitZoom("reset", 1)
}

// The main screen at 1.5×: the explorer, the editor and the statusline hold their larger text.
func TestZoomedIDESnapshot(t *testing.T) {
	s, r := apptest.ExplorerScene(t)
	s.Zoom(1.5)
	s.Frame()
	s.Frame()
	s.Run("view.toggle", registry.Args{"id": kit.ExplorerView, "place": "dock", "edge": "left"})
	s.Run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal", "pricing", "round.go")})
	s.WaitHighlights()
	s.SettleSigns() // the gutter's signs and the line's blame, which come off the loop
	s.Golden("ide-zoom")
}

// A part with its own size (ui.statusline.font_size) is drawn at it over the UI's, bar and
// text alike; unset, it follows ui.font_size.
func TestPartFontSizes(t *testing.T) {
	s := apptest.EditorScene(t)
	o := s.Registry().Options()
	o.Set("ui.font_size", registry.LayerUser, registry.At{}, 16.0)
	if v, _, _ := o.Get("ui.statusline.font_size", registry.At{}); v != 16.0 || kit.PartScale(o, "ui.statusline.font_size") != 0 {
		t.Errorf("unset under ui.font_size 16: reads %v, scale %v; want 16, following", v, kit.PartScale(o, "ui.statusline.font_size"))
	}
	o.Unset("ui.font_size", registry.LayerUser, registry.At{})
	normal := func() (size float32, bar render.Rect) {
		s.Frame()
		for _, op := range s.List().Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == "NORMAL" {
				size = op.Run.Font.Size
			}
		}
		for _, op := range s.List().Ops {
			if op.Kind == render.OpRect && op.Rect.Max.Y == s.Size().Y && op.Rect.Dx() == s.Size().X && op.Rect.Dy() < 100 {
				bar = op.Rect // the status line's own fill, along the bottom
			}
		}
		return size, bar
	}
	size0, bar0 := normal()
	if size0 == 0 {
		t.Fatal("no NORMAL in the status line")
	}
	s.Registry().Options().Set("ui.statusline.font_size", registry.LayerUser, registry.At{}, 19.5)
	size1, bar1 := normal()
	if size1 != size0*1.5 || bar1.Dy() != bar0.Dy()*1.5 {
		t.Errorf("at 19.5 over the UI's 13: NORMAL %v px on a %v px bar, want %v on %v", size1, bar1.Dy(), size0*1.5, bar0.Dy()*1.5)
	}
}
