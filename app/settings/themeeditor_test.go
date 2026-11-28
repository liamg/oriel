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
	s.Run("theme.edit", registry.Args{"args": "pick"})
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

// The preview outlines and names the token under the pointer, a click picks that token, and the mark goes when
// the pointer leaves.
func TestThemePreviewHover(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("theme.select", registry.Args{"name": "accent"})
	s.Frame()
	s.Frame()
	preview := s.Node("Preview in oriel").Bounds
	tag := func() *render.AccessNode {
		var find func(n *render.AccessNode) *render.AccessNode
		find = func(n *render.AccessNode) *render.AccessNode {
			if n.Role == render.RoleStatus && n.Bounds.Min.Y >= preview.Min.Y-1 && n.Bounds.Min.X >= preview.Min.X && n.Bounds.Min.X < preview.Max.X {
				return n
			}
			for _, k := range n.Children {
				if f := find(k); f != nil {
					return f
				}
			}
			return nil
		}
		return find(s.Access())
	}
	move := func(p render.Point) {
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		s.Frame()
		s.Frame()
	}
	at := render.Point{X: preview.Min.X + 200, Y: preview.Min.Y + 120}
	move(at)
	n := tag()
	if n == nil || n.Name == "" {
		t.Fatalf("nothing named under the pointer in the preview:\n%s", s.Text())
	}
	if _, ok := s.UI().Theme().Link(n.Name); !ok && !strings.HasPrefix(n.Name, "palette:") && !strings.HasPrefix(n.Name, "@") {
		t.Errorf("the tag %q isn't a token", n.Name)
	}
	s.Click(at, 0)
	if te := themeEditor(s); te.Picked() != n.Name {
		t.Errorf("clicked under %q, picked %q", n.Name, te.Picked())
	}
	move(render.Point{X: 5, Y: preview.Min.Y + 5})
	if n := tag(); n != nil {
		t.Errorf("a tag outside the preview: %q", n.Name)
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

// Theme choices made in the editor reach a later app on the same config: a restart, or a new workspace.
func TestThemeEditorSavesThemeChoices(t *testing.T) {
	// later starts another app on s's config directory.
	later := func(t *testing.T, s *apptest.Scene) *apptest.Scene {
		t.Helper()
		return apptest.New(t, apptest.Options{WorkDir: t.TempDir(), ConfigDir: s.ConfigDir})
	}
	option := func(s *apptest.Scene, name string) any {
		v, _, _ := s.Registry().Options().Get(name, registry.At{})
		return v
	}
	t.Run("an override of a built-in theme", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.Run("settings.set", registry.Args{"name": "ui.theme", "value": "tokyonight", "scope": "user"})
		s.Run("theme.set", registry.Args{"name": "requests.avatar.you", "value": "#0000ff"})
		next := later(t, s)
		next.Retheme()
		if v := option(next, "ui.theme"); v != "tokyonight-local" {
			t.Errorf("ui.theme is %v in the later app", v)
		}
		if c, _ := next.UI().Theme().Color("requests.avatar.you"); theme.Hex(c) != "#0000ff" {
			t.Errorf("requests.avatar.you is %s in the later app", theme.Hex(c))
		}
	})
	t.Run(":Theme with a name", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.Run("theme.edit", registry.Args{"args": "tokyonight"})
		if v := option(later(t, s), "ui.theme"); v != "tokyonight" {
			t.Errorf("ui.theme is %v in the later app", v)
		}
	})
	t.Run("the variant toggle is a preview", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.Run("settings.set", registry.Args{"name": "ui.variant", "value": "dark", "scope": "user"})
		s.Run("theme.edit", nil)
		s.Frame()
		s.Frame()
		s.Click(apptest.Centre(s.Node("LIGHT").Bounds), 0)
		if v, l, _ := s.Registry().Options().Get("ui.variant", registry.At{}); v != "light" || l != registry.LayerPreview {
			t.Fatalf("after the toggle: %v from %v", v, l)
		}
		// The first override on a built-in theme writes settings.toml, which reads the file back in.
		s.Run("theme.set", registry.Args{"name": "requests.avatar.you", "value": "#0000ff"})
		s.Frame()
		if v := option(s, "ui.variant"); v != "light" {
			t.Errorf("settings.toml read back over the preview: %v", v)
		}
		s.Type("Esc")
		s.Frame()
		if v, l, _ := s.Registry().Options().Get("ui.variant", registry.At{}); v != "dark" || l != registry.LayerUser {
			t.Errorf("after closing the editor: %v from %v", v, l)
		}
		if v := option(later(t, s), "ui.variant"); v != "dark" {
			t.Errorf("ui.variant is %v in the later app", v)
		}
	})
}

// A theme with one variant keeps it: its local theme takes overrides in that variant's tables, and the editor names
// the variant in place of a toggle that couldn't change it.
func TestThemeEditorOnASingleVariantTheme(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("settings.set", registry.Args{"name": "ui.theme", "value": "tokyonight", "scope": "user"})
	s.Retheme()
	s.Run("theme.edit", nil)
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "DARK ONLY") || strings.Contains(s.Text(), "LIGHT") {
		t.Errorf("the toolbar on tokyonight:\n%s", s.Text())
	}
	s.Run("theme.set", registry.Args{"name": "requests.avatar.you", "value": "#0000ff"})
	b, err := os.ReadFile(filepath.Join(s.ConfigDir, "themes", "tokyonight-local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "name = \"tokyonight-local\"\nextends = \"tokyonight\"\n\n[dark.component]\n\"requests.avatar.you\" = \"#0000ff\"\n"; got != want {
		t.Errorf("wrote:\n%s\nwant:\n%s", got, want)
	}
	if err := s.Registry().Options().Set("ui.variant", registry.LayerUser, registry.At{}, "light"); err != nil {
		t.Fatal(err)
	}
	s.Retheme()
	if th := s.UI().Theme(); th.Name != "tokyonight-local" || th.Variant != theme.Dark {
		t.Errorf("asked for light: %s %s", th.Name, th.Variant)
	}
}

// A local theme written with [dark] overrides over a dark-only theme stays dark-only, and the editor changes its
// values in place.
func TestThemeEditorOnAnOlderLocalTheme(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.WriteConfig(map[string]string{"themes/tokyonight-local.toml": "name = \"tokyonight-local\"\nextends = \"tokyonight\"\n\n[dark.component]\n\"requests.avatar.you\" = \"#0000ff\"\n"})
	s = apptest.New(t, apptest.Options{WorkDir: t.TempDir(), ConfigDir: s.ConfigDir})
	s.Run("settings.set", registry.Args{"name": "ui.theme", "value": "tokyonight-local", "scope": "user"})
	if err := s.Registry().Options().Set("ui.variant", registry.LayerUser, registry.At{}, "light"); err != nil {
		t.Fatal(err)
	}
	s.Retheme()
	th := s.UI().Theme()
	if c, _ := th.Color("requests.avatar.you"); th.Variant != theme.Dark || theme.Hex(c) != "#0000ff" {
		t.Errorf("asked for light: %s, avatar %s", th.Variant, theme.Hex(c))
	}
	s.Run("theme.set", registry.Args{"name": "requests.avatar.you", "value": "#00ff00"})
	b, err := os.ReadFile(filepath.Join(s.ConfigDir, "themes", "tokyonight-local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Count(got, "requests.avatar.you") != 1 || !strings.Contains(got, "[dark.component]\n\"requests.avatar.you\" = \"#00ff00\"") {
		t.Errorf("after the edit:\n%s", got)
	}
}

// Your own theme that names one variant keeps it when you edit it.
func TestThemeEditorOnYourOwnSingleVariantTheme(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.WriteConfig(map[string]string{"themes/mine.toml": "name = \"mine\"\nextends = \"oriel\"\nvariant = \"dark\"\n"})
	s = apptest.New(t, apptest.Options{WorkDir: t.TempDir(), ConfigDir: s.ConfigDir})
	s.Run("settings.set", registry.Args{"name": "ui.theme", "value": "mine", "scope": "user"})
	s.Run("theme.set", registry.Args{"name": "requests.avatar.you", "value": "#0000ff"})
	if err := s.Registry().Options().Set("ui.variant", registry.LayerUser, registry.At{}, "light"); err != nil {
		t.Fatal(err)
	}
	s.Retheme()
	th := s.UI().Theme()
	if c, _ := th.Color("requests.avatar.you"); th.Name != "mine" || th.Variant != theme.Dark || theme.Hex(c) != "#0000ff" {
		t.Errorf("asked for light: %s %s, avatar %s", th.Name, th.Variant, theme.Hex(c))
	}
}
