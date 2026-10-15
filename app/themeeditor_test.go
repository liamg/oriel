package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func (s *errScene) themeEditor() *themeEditor {
	for _, e := range s.exts {
		if t, ok := e.(*themeEditor); ok {
			return t
		}
	}
	return nil
}

// retheme resolves ui.theme and ui.variant from the registry, as the loop does when a theme changes.
func (s *errScene) retheme() {
	s.t.Helper()
	name, _, _ := s.reg.Options().Get("ui.theme", registry.At{})
	v := theme.Dark
	if got, _, _ := s.reg.Options().Get("ui.variant", registry.At{}); got == "light" {
		v = theme.Light
	}
	srcs, _ := s.reg.Themes()
	r, err := resolveTheme(srcs, name.(string), v)
	if err != nil {
		s.t.Fatal(err)
	}
	s.ui.SetTheme(r)
}

func (s *errScene) access(label string) *render.AccessNode {
	s.t.Helper()
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	n := find(s.ui.AccessTree())
	if n == nil {
		s.t.Fatalf("nothing labelled %q", label)
	}
	return n
}

func (s *errScene) click(p render.Point) { s.clickWith(p, 0) }

// clickWith clicks at p with modifier keys held.
func (s *errScene) clickWith(p render.Point, mods render.Mods) {
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary, Mods: mods})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary, Mods: mods})
	s.frame()
}

func (s *errScene) typeKeys(keys ...string) {
	for _, k := range keys {
		if !s.ui.Key(render.KeyEvent{Key: k}) {
			s.t.Fatalf("nothing focused takes %q", k)
		}
		s.frame()
	}
}

func centre(r render.Rect) render.Point {
	return render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
}

// 23-theme: the editor on bg.raised, against a golden.
func TestThemeEditorSnapshot(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.run("theme.select", registry.Args{"name": "bg.raised"})
	s.golden("theme-editor")
	text := s.frameText()
	for _, want := range []string{"PICKED", "bg.raised", "PALETTE", "SEMANTIC", "CONTRAST", "WRITING TO"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in the editor:\n%s", want, text)
		}
	}
}

var tTestSwatch = theme.Declare("test.swatch", "accent")

// Pick mode: hovering the live window names the token that painted what's under the pointer, and a click picks it,
// even where another token has the same colour.
func TestThemePickMode(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.reg.View(OverlayPrefix+"swatch", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "swatch", Fn: func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(80), H: ui.Px(40), Fill: c.Theme().C(tTestSwatch), Z: 500, Float: &ui.Float{Point: &render.Point{X: 700, Y: 500}}}, nil)
		}}
	}))
	s.frame()
	s.run("Theme", registry.Args{"args": "pick"})
	te := s.themeEditor()
	if !te.picking {
		t.Fatal("not picking")
	}
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 800, Y: 990}})
	s.frame()
	s.frame()
	if te.hover != "statusline.bg" {
		t.Errorf("over the status line: %q", te.hover)
	}
	s.click(render.Point{X: 740, Y: 520})
	if te.picking || !te.open || te.picked != "test.swatch" {
		t.Fatalf("after the click: picking %v, open %v, picked %q", te.picking, te.open, te.picked)
	}
	s.frame()
	if !strings.Contains(s.frameText(), "test.swatch") {
		t.Errorf("the editor doesn't show what was picked:\n%s", s.frameText())
	}
}

// Edits go into a local theme that extends the built-in one, as the smallest diff; undo and reset take them back.
func TestThemeEditorWritesOverrides(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "themes", "oriel-local.toml")
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	s.run("theme.select", registry.Args{"name": "accent"})
	s.run("theme.set", registry.Args{"name": "accent", "value": "#ff0000"})
	if got, want := read(), "name = \"oriel-local\"\nextends = \"oriel\"\n\n[dark.semantic]\naccent = \"#ff0000\"\n"; got != want {
		t.Fatalf("wrote:\n%s\nwant:\n%s", got, want)
	}
	if name, _, _ := s.reg.Options().Get("ui.theme", registry.At{}); name != "oriel-local" {
		t.Errorf("ui.theme is %v", name)
	}
	s.retheme()
	if c, _ := s.ui.Theme().Color("accent"); theme.Hex(c) != "#ff0000" {
		t.Errorf("accent resolved to %s", theme.Hex(c))
	}

	// The derive field: type an expression, Enter writes it.
	s.frame()
	s.frame()
	s.click(centre(s.access("Expression").Bounds))
	keys := []string{"End"}
	for range len("#ff0000") {
		keys = append(keys, "BS")
	}
	for _, r := range "brand" {
		keys = append(keys, string(r))
	}
	s.typeKeys(append(keys, "CR")...)
	if got := read(); !strings.Contains(got, "accent = \"brand\"") || strings.Count(got, "accent") != 1 {
		t.Fatalf("after typing:\n%s", got)
	}

	s.run("theme.undo", nil)
	if got := read(); !strings.Contains(got, "accent = \"#ff0000\"") {
		t.Errorf("after undo:\n%s", got)
	}
	s.run("theme.reset", registry.Args{"name": "accent"})
	if got := read(); strings.Contains(got, "accent") || strings.Contains(got, "[dark.semantic]") {
		t.Errorf("after reset:\n%s", got)
	}
	if err := s.reg.Run(t.Context(), "theme.set", registry.Args{"name": "accent", "value": "mix(ink"}); err == nil {
		t.Error("a broken expression was written")
	}
}

// Pick mode over the editor: code reads back as its syntax group.
func TestThemePickSyntax(t *testing.T) {
	s := editorScene(t)
	s.run("Theme", registry.Args{"args": "pick"})
	te := s.themeEditor()
	var at render.Point
	found := false
	for y := te.probe.Rect.Min.Y; y < te.probe.Rect.Max.Y && !found; y++ {
		for x := te.probe.Rect.Min.X; x < te.probe.Rect.Max.X; x++ {
			c := te.probe.RGBAAt(x, y)
			if name, ok := te.probeTh.Probed(render.Color{R: c.R, G: c.G, B: c.B, A: 255}); ok && name == "@keyword" {
				at, found = render.Point{X: float32(x) + .5, Y: float32(y) + .5}, true
				break
			}
		}
	}
	if !found {
		t.Fatal("no @keyword painted in the probe")
	}
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.golden("theme-pick")
	if te.hover != "@keyword" {
		t.Errorf("over a keyword: %q", te.hover)
	}
}
