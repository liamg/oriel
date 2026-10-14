package app

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// theming.md, "Sizes": ui.font_size zooms the window, so every part grows together; ⌘= ⌘- ⌘0 step it.
func TestUIFontSizeZoomsTheWindow(t *testing.T) {
	r := startApp(t, t.TempDir(), "", nil)
	before := r.win.Size()
	waitZoom := func(what string, z float32) {
		t.Helper()
		want := render.Point{X: before.X / z, Y: before.Y / z}
		deadline := time.Now().Add(5 * time.Second)
		for r.win.Scale() != z || r.win.Size() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: scale %v, size %v; want %v, %v", what, r.win.Scale(), r.win.Size(), z, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	r.do(func() error {
		return r.host.Options().Set("ui.font_size", registry.LayerUser, registry.At{}, 19.5)
	})
	waitZoom("ui.font_size 19.5", 1.5)
	font := func() float64 {
		var v any
		r.do(func() error { v, _, _ = r.host.Options().Get("ui.font_size", registry.At{}); return nil })
		return v.(float64)
	}
	for _, step := range []struct {
		cmd  string
		want float64
	}{{"ui.zoom.in", 20.5}, {"ui.zoom.out", 19.5}, {"ui.zoom.out", 18.5}, {"ui.zoom.reset", 13}} {
		r.do(func() error { return r.host.Run(context.Background(), step.cmd, nil) })
		if got := font(); math.Abs(got-step.want) > 1e-9 {
			t.Errorf("after %s: ui.font_size %v, want %v", step.cmd, got, step.want)
		}
	}
	waitZoom("reset", 1)
}

// theming.md, "Sizes": editor.font_size and editor.line_height are the code's own, and the rows follow them.
func TestEditorFontSize(t *testing.T) {
	s := editorScene(t)
	if m := s.editor().pane; m.font.Size != 13.5 || m.lineH != 21 {
		t.Fatalf("default code %v px on %v px rows, want 13.5 on 21 (01-ide)", m.font.Size, m.lineH)
	}
	s.reg.Options().Set("editor.font_size", registry.LayerUser, registry.At{}, 18.0)
	s.reg.Options().Set("editor.line_height", registry.LayerUser, registry.At{}, 1.5)
	s.frame()
	m := s.editor().pane
	if m.font.Size != 18 || m.lineH != 27 {
		t.Errorf("code %v px on %v px rows, want 18 on 27", m.font.Size, m.lineH)
	}
	sizes := map[float32]bool{}
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Font.Family == typeset.Mono {
			sizes[op.Run.Font.Size] = true
		}
	}
	if !sizes[18] {
		t.Errorf("no code drawn at 18 px: %v", sizes)
	}
}

// The main screen at 1.5×: the explorer, the editor and the statusline hold their larger text.
func TestZoomedIDESnapshot(t *testing.T) {
	s, r := explorerScene(t)
	s.zoom(1.5)
	s.run("view.toggle", registry.Args{"id": ExplorerView, "place": "dock", "edge": "left"})
	s.run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal", "pricing", "round.go")})
	s.waitHighlights()
	s.golden("ide-zoom")
}

// theming.md, "Sizes": a part with its own size (ui.statusline.font_size) is drawn at it over the UI's, bar and
// text alike; 0 follows ui.font_size.
func TestPartFontSizes(t *testing.T) {
	s := editorScene(t)
	normal := func() (size float32, bar render.Rect) {
		s.frame()
		for _, op := range s.list.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == "NORMAL" {
				size = op.Run.Font.Size
			}
		}
		for _, op := range s.list.Ops {
			if op.Kind == render.OpRect && op.Rect.Max.Y == s.size.Y && op.Rect.Dx() == s.size.X && op.Rect.Dy() < 100 {
				bar = op.Rect // the status line's own fill, along the bottom
			}
		}
		return size, bar
	}
	size0, bar0 := normal()
	if size0 == 0 {
		t.Fatal("no NORMAL in the status line")
	}
	s.reg.Options().Set("ui.statusline.font_size", registry.LayerUser, registry.At{}, 19.5)
	size1, bar1 := normal()
	if size1 != size0*1.5 || bar1.Dy() != bar0.Dy()*1.5 {
		t.Errorf("at 19.5 over the UI's 13: NORMAL %v px on a %v px bar, want %v on %v", size1, bar1.Dy(), size0*1.5, bar0.Dy()*1.5)
	}
}
