package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func themeEditor(s *apptest.Scene) *settings.ThemeEditor {
	return apptest.PluginOf[*settings.ThemeEditor](s, "theme")
}

// 23-theme: the editor on bg.raised, against a golden.
func TestThemeEditorSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("theme.select", registry.Args{"name": "bg.raised"})
	s.Golden("theme-editor")
	text := s.Text()
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
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Registry().View(kit.OverlayPrefix+"swatch", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "swatch", Fn: func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(80), H: ui.Px(40), Fill: c.Theme().C(tTestSwatch), Z: 500, Float: &ui.Float{Point: &render.Point{X: 700, Y: 500}}}, nil)
		}}
	}))
	s.Frame()
	s.Run("Theme", registry.Args{"args": "pick"})
	te := themeEditor(s)
	if !te.Picking() {
		t.Fatal("not picking")
	}
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 800, Y: 990}})
	s.Frame()
	s.Frame()
	if te.Hover() != "statusline.bg" {
		t.Errorf("over the status line: %q", te.Hover())
	}
	s.Click(render.Point{X: 740, Y: 520}, 0)
	if te.Picking() || !te.Open() || te.Picked() != "test.swatch" {
		t.Fatalf("after the click: picking %v, open %v, picked %q", te.Picking(), te.Open(), te.Picked())
	}
	s.Frame()
	if !strings.Contains(s.Text(), "test.swatch") {
		t.Errorf("the editor doesn't show what was picked:\n%s", s.Text())
	}
}

// Edits go into a local theme that extends the built-in one, as the smallest diff; undo and reset take them back.
func TestThemeEditorWritesOverrides(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	path := filepath.Join(s.ConfigDir, "themes", "oriel-local.toml")
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	s.Run("theme.select", registry.Args{"name": "accent"})
	s.Run("theme.set", registry.Args{"name": "accent", "value": "#ff0000"})
	if got, want := read(), "name = \"oriel-local\"\nextends = \"oriel\"\n\n[dark.semantic]\naccent = \"#ff0000\"\n"; got != want {
		t.Fatalf("wrote:\n%s\nwant:\n%s", got, want)
	}
	if name, _, _ := s.Registry().Options().Get("ui.theme", registry.At{}); name != "oriel-local" {
		t.Errorf("ui.theme is %v", name)
	}
	s.Retheme()
	if c, _ := s.UI().Theme().Color("accent"); theme.Hex(c) != "#ff0000" {
		t.Errorf("accent resolved to %s", theme.Hex(c))
	}

	// The derive field: type an expression, Enter writes it.
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("Expression").Bounds), 0)
	keys := []string{"End"}
	for range len("#ff0000") {
		keys = append(keys, "BS")
	}
	for _, r := range "brand" {
		keys = append(keys, string(r))
	}
	s.Type(append(keys, "CR")...)
	if got := read(); !strings.Contains(got, "accent = \"brand\"") || strings.Count(got, "accent") != 1 {
		t.Fatalf("after typing:\n%s", got)
	}

	s.Run("theme.undo", nil)
	if got := read(); !strings.Contains(got, "accent = \"#ff0000\"") {
		t.Errorf("after undo:\n%s", got)
	}
	s.Run("theme.reset", registry.Args{"name": "accent"})
	if got := read(); strings.Contains(got, "accent") || strings.Contains(got, "[dark.semantic]") {
		t.Errorf("after reset:\n%s", got)
	}
	if err := s.Registry().Run(t.Context(), "theme.set", registry.Args{"name": "accent", "value": "mix(ink"}); err == nil {
		t.Error("a broken expression was written")
	}
}
