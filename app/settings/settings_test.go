package settings_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const handWritten = `# my settings, by hand

[ui]
# the theme I like
theme = "oriel"   # keep

[whichkey]
enabled = true
`

// A GUI change touches only the line it changes, so a dotfiles diff is one line, and reset removes the line from
// the file. Comments, order and spacing written by hand survive.
func TestSettingsEditOneLine(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	path := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, path, handWritten)
	settingsOf(s).Reload()
	step := func(what string, wantRemoved, wantAdded int, do func()) string {
		t.Helper()
		before := rig.ReadFile(t, path)
		do()
		after := rig.ReadFile(t, path)
		if r, a := rig.Changed(before, after); r != wantRemoved || a != wantAdded {
			t.Errorf("%s: %d lines removed and %d added, want %d and %d:\n%s", what, r, a, wantRemoved, wantAdded, after)
		}
		return after
	}
	got := step("change a value", 1, 1, func() { s.Run("settings.set", registry.Args{"name": "ui.theme", "value": "mine"}) })
	if !strings.Contains(got, "theme = \"mine\"   # keep") || !strings.Contains(got, "# the theme I like") {
		t.Errorf("the line's comment or the one above went:\n%s", got)
	}
	got = step("add a key", 0, 1, func() { s.Run("settings.set", registry.Args{"name": "ui.reduced_motion", "value": true}) })
	if !strings.Contains(got, "theme = \"mine\"   # keep\nreduced_motion = true\n") {
		t.Errorf("the new key isn't in its table:\n%s", got)
	}
	got = step("reset", 1, 0, func() { s.Run("settings.reset", registry.Args{"name": "ui.theme"}) })
	if strings.Contains(got, "theme =") || !strings.Contains(got, "# the theme I like") {
		t.Errorf("after reset:\n%s", got)
	}
	if v, l, _ := s.Registry().Options().Get("ui.theme", registry.At{}); v != "oriel" || l != registry.LayerDefault {
		t.Errorf("after reset ui.theme is %v from %v", v, l)
	}
	// The page's toggle is the same edit.
	s.Run("settings.open", registry.Args{"section": "whichkey"})
	s.Frame()
	s.Frame()
	step("toggle on the page", 1, 1, func() { s.Click(apptest.Centre(s.Node("whichkey.enabled").Bounds), 0) })
	if v, _, _ := s.Registry().Options().Get("whichkey.enabled", registry.At{}); v != false {
		t.Errorf("whichkey.enabled is %v", v)
	}
	s.Run("settings.undo", nil)
	if got := rig.ReadFile(t, path); !strings.Contains(got, "enabled = true") {
		t.Errorf("undo:\n%s", got)
	}
}

// docs/configuration.md, "Precedence": defaults, the user file, init.lua, the project file, the session. A project
// file is trusted on first open, and again whenever it changes.
func TestSettingsPrecedence(t *testing.T) {
	work := t.TempDir()
	s := apptest.New(t, apptest.Options{WorkDir: work})
	s.Registry().Option(registry.OptionSpec{Name: "test.level", Type: registry.String, Default: "oriel",
		Scopes: registry.ScopeUser | registry.ScopeProject | registry.ScopeSession})
	o := s.Registry().Options()
	user := filepath.Join(s.ConfigDir, "settings.toml")
	project := filepath.Join(work, ".oriel", "settings.toml")
	here := registry.At{Project: work}
	expect := func(at registry.At, want any, layer registry.Layer, source string) {
		t.Helper()
		v, l, _ := o.Get("test.level", at)
		if v != want || l != layer || o.Source("test.level", at) != source {
			t.Errorf("test.level = %v from %v (%q), want %v from %v (%q)", v, l, o.Source("test.level", at), want, layer, source)
		}
	}
	rig.WriteFile(t, user, "[test]\nlevel = \"from-user\"\n")
	settingsOf(s).Reload()
	expect(here, "from-user", registry.LayerUser, user)

	o.SetFrom("test.level", registry.LayerLua, registry.At{}, "from-lua", "init.lua:12")
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")

	rig.WriteFile(t, project, "[test]\nlevel = \"from-project\"\n")
	settingsOf(s).Reload()
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")
	n := s.Store().State().Notifications
	if len(n) == 0 || n[len(n)-1].ID != "settings.untrusted" || len(n[len(n)-1].Actions) == 0 || n[len(n)-1].Actions[0].Command != "settings.trust" {
		t.Fatalf("no notice offering to trust the project's settings: %+v", n)
	}
	s.Run("settings.trust", registry.Args{"path": project})
	expect(here, "from-project", registry.LayerProject, project)
	expect(registry.At{}, "from-lua", registry.LayerLua, "init.lua:12")

	rig.WriteFile(t, project, "[test]\nlevel = \"changed\"\n")
	settingsOf(s).Reload()
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")

	o.Set("test.level", registry.LayerSession, registry.At{Session: "s1"}, "from-session")
	expect(registry.At{Project: work, Session: "s1"}, "from-session", registry.LayerSession, "")

	o.Unset("test.level", registry.LayerLua, registry.At{})
	rig.WriteFile(t, user, "[test]\n")
	settingsOf(s).Reload()
	expect(registry.At{}, "oriel", registry.LayerDefault, "")
}

// A value set in Lua is read-only on the page, with a jump to the line.
func TestSettingsLuaIsReadOnly(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	path := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, path, handWritten)
	settingsOf(s).Reload()
	s.Registry().Options().SetFrom("whichkey.enabled", registry.LayerLua, registry.At{}, false, "init.lua:12")
	err := s.Registry().Run(context.Background(), "settings.set", registry.Args{"name": "whichkey.enabled", "value": true})
	if err == nil || !strings.Contains(err.Error(), "init.lua:12") {
		t.Errorf("setting a Lua-set option: %v", err)
	}
	if got := rig.ReadFile(t, path); got != handWritten {
		t.Errorf("the file changed:\n%s", got)
	}
	s.Run("settings.open", registry.Args{"section": "whichkey"})
	s.Frame()
	s.Frame()
	if n := s.Node("whichkey.enabled"); !n.Disabled {
		t.Error("the toggle isn't disabled")
	}
	text := s.Text()
	for _, want := range []string{"init.lua:12", "init.lua wins over settings.toml", "Open init.lua:12"} {
		if !strings.Contains(text, want) {
			t.Errorf("the row lacks %q:\n%s", want, text)
		}
	}
	if s.Node("Open init.lua:12") == nil {
		t.Error("no link to the line")
	}
}

// docs/configuration.md, "Precedence": the project file and the session come after init.lua, so at those scopes a
// Lua-set option is editable, and what's set there wins.
func TestSettingsLuaIsOverriddenAboveIt(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	settingsOf(s).Reload()
	s.Registry().Options().SetFrom("editor.expand_tab", registry.LayerLua, registry.At{}, false, "init.lua:12")
	if err := s.Registry().Run(context.Background(), "settings.set", registry.Args{"name": "editor.expand_tab", "value": true, "scope": "session"}); err != nil {
		t.Fatalf("setting it for the session: %v", err)
	}
	if v, l, _ := s.Registry().Options().Get("editor.expand_tab", s.Host().Here()); v != true || l != registry.LayerSession {
		t.Errorf("editor.expand_tab = %v from %s", v, l)
	}
	s.Run("settings.open", registry.Args{"section": "editor"})
	s.Frame()
	s.Frame()
	s.ClickText("SESSION")
	s.Frame()
	if n := s.Node("editor.expand_tab"); n.Disabled {
		t.Error("the toggle is disabled at session scope")
	}
}

// A parse error keeps the last good values and says where, and nothing is written back.
func TestSettingsParseError(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	path := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, path, "[ui]\ntheme = \"good\"\n")
	settingsOf(s).Reload()
	rig.WriteFile(t, path, "[ui]\ntheme = = \"bad\"\n")
	settingsOf(s).Reload()
	if v, _, _ := s.Registry().Options().Get("ui.theme", registry.At{}); v != "good" {
		t.Errorf("ui.theme = %v", v)
	}
	n := s.Store().State().Notifications
	if len(n) == 0 || !strings.Contains(n[len(n)-1].Text, "2:9") {
		t.Errorf("no notice with the line and column: %+v", n)
	}
	if err := s.Registry().Run(context.Background(), "settings.set", registry.Args{"name": "ui.theme", "value": "x"}); err == nil {
		t.Error("wrote to a file that doesn't parse")
	}
	if got := rig.ReadFile(t, path); got != "[ui]\ntheme = = \"bad\"\n" {
		t.Errorf("the file changed: %q", got)
	}
}

// The page, against a golden.
func TestSettingsSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	rig.WriteFile(t, filepath.Join(s.ConfigDir, "settings.toml"), handWritten)
	settingsOf(s).Reload()
	s.Registry().Options().SetFrom("ui.reduced_motion", registry.LayerLua, registry.At{}, true, "init.lua:12")
	s.Run("Settings", registry.Args{"args": "ui"})
	s.Golden("settings")
}

// settingsAt is the settings page searched for query, drawn.
func settingsAt(t *testing.T, query string) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("settings.open", registry.Args{"query": query})
	s.Frame()
	s.Frame()
	return s
}

func TestSettingsInputIsAMenuOfEngines(t *testing.T) {
	s := settingsAt(t, "editor.input")
	s.ClickText("vim ▾")
	s.ClickText("plain")
	if v, _, _ := s.Registry().Options().Get("editor.input", registry.At{}); v != "plain" {
		t.Errorf("picking plain from editor.input's menu: %v", v)
	}
}

func TestSettingsEscapeAfterAToggle(t *testing.T) {
	s := settingsAt(t, "editor.expand_tab")
	s.Click(apptest.Centre(s.Node("editor.expand_tab").Bounds), 0)
	if !s.Shown("settings") {
		t.Fatal("clicking a toggle closed the page")
	}
	s.Type("Esc")
	s.Frame()
	if s.Shown("settings") {
		t.Error("Escape with a toggle focused didn't close the settings page")
	}
}

// A field sets on Enter, when it loses focus, or once typing pauses, and only a value that's right.
func TestSettingsNumberField(t *testing.T) {
	s := settingsAt(t, "editor.max_line_length")
	get := func() any { v, _, _ := s.Registry().Options().Get("editor.max_line_length", registry.At{}); return v }
	s.Click(apptest.Centre(s.Node("editor.max_line_length").Bounds), 0)
	s.Type("End", "1", "x", "2")
	if v := s.Node("editor.max_line_length").Value; v != "012" {
		t.Errorf("typing 1 x 2 into a whole number's field: %q, want 012", v)
	}
	if v := get(); v != 0 {
		t.Errorf("set to %v while still typing", v)
	}
	s.Now = s.Now.Add(settings.TypingPause)
	s.Frame()
	if v := get(); v != 12 {
		t.Errorf("after a pause, editor.max_line_length = %v", v)
	}
	s.Frame()
	if !strings.Contains(s.Text(), "✓ saved") {
		t.Error("the row doesn't say it was saved")
	}
	s.Type("0", "0", "0", "0")
	s.Frame()
	if !strings.Contains(s.Text(), "is between") {
		t.Error("an out-of-range value doesn't say why under the field")
	}
	s.Now = s.Now.Add(settings.TypingPause)
	s.Frame()
	s.Type("CR")
	if v := get(); v != 12 {
		t.Errorf("an out-of-range value was set: %v", v)
	}
	s.Type("Esc")
	s.Frame()
	if v := s.Node("editor.max_line_length").Value; v != "12" {
		t.Errorf("Escape leaves %q in the field, want it back to 12", v)
	}
	s.Click(apptest.Centre(s.Node("editor.max_line_length").Bounds), 0)
	s.Type("End", "BS", "3", "CR")
	if v := get(); v != 13 {
		t.Errorf("after Enter, editor.max_line_length = %v", v)
	}
}

// A number with a range and a step has a slider, set when it's let go; the field beside it shows where it is.
func TestSettingsSlider(t *testing.T) {
	s := settingsAt(t, "editor.font_size")
	get := func() any { v, _, _ := s.Registry().Options().Get("editor.font_size", registry.At{}); return v }
	r := s.Node("editor.font_size 13.5").Bounds
	y := (r.Min.Y + r.Max.Y) / 2
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: r.Min.X + 1, Y: y}})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: r.Min.X + 1, Y: y}, Buttons: render.ButtonPrimary})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: r.Max.X + 40, Y: y}, Buttons: render.ButtonPrimary})
	s.Frame()
	if v := s.Node("editor.font_size").Value; v != "48" {
		t.Errorf("dragged past the end, the field shows %q", v)
	}
	if v := get(); v != 13.5 {
		t.Errorf("set to %v while still dragging", v)
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: r.Max.X + 40, Y: y}, Buttons: render.ButtonPrimary})
	s.Frame()
	if v := get(); v != 48.0 {
		t.Errorf("let go at the end: editor.font_size = %v", v)
	}
	s.Type("h")
	if v := get(); v != 47.5 {
		t.Errorf("a step down from the keyboard: %v", v)
	}
}

// A part's font size shows the UI's while it follows it, and says so.
func TestSettingsFollows(t *testing.T) {
	s := settingsAt(t, "ui.tooltip.font_size")
	s.Registry().Options().Set("ui.font_size", registry.LayerUser, registry.At{}, 15.0)
	s.Frame()
	s.Frame()
	if v := s.Node("ui.tooltip.font_size").Value; v != "15" {
		t.Errorf("following ui.font_size at 15, the field shows %q", v)
	}
	if !strings.Contains(s.Text(), "follows ui.font_size") {
		t.Error("the row doesn't say it follows ui.font_size")
	}
}

// fontShown shows editor.font_size at the window's directory in the status line.
type fontShown struct{}

func (fontShown) Manifest() plugin.Manifest        { return plugin.Manifest{ID: "test/font-shown"} }
func (fontShown) Deactivate(context.Context) error { return nil }
func (fontShown) Activate(_ context.Context, h plugin.Host) error {
	h.View("statusline.right.99-font", uireg.GlobalView{View: uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		v, _, _ := h.Options().Get("editor.font_size", h.Here())
		return &ui.Text{Text: fmt.Sprintf("font %v", v)}
	})})
	return nil
}

// The window moving onto a directory waits behind its opening cover for the project's settings there: the first
// frame laid out on it has them.
func TestAMoveBringsTheProjectsSettings(t *testing.T) {
	w := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: t.TempDir(), Plugins: []plugin.Plugin{fontShown{}}})
	dir := t.TempDir()
	path := filepath.Join(dir, ".oriel", "settings.toml")
	src := []byte("[editor]\nfont_size = 21\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (settingsfile.Trust{Path: filepath.Join(w.ConfigDir, "trusted.toml")}).Add(path, src); err != nil {
		t.Fatal(err)
	}
	w.WaitText("font 13.5")
	before := len(w.Win.Frames())
	if err := w.Do(func() error {
		win, _ := kit.WindowOf(w.Host)
		return win.Reopen(dir, "")
	}); err != nil {
		t.Fatal(err)
	}
	w.WaitFor("the window laid out on "+dir+" with its project's settings", func() bool {
		fs := w.Win.Frames()
		x := fs[len(fs)-1].Text()
		return strings.Contains(x, "font 21") && !strings.Contains(x, "Opening")
	})
	fs := w.Win.Frames()[before:]
	opened := false
	for _, f := range fs {
		text := f.Text()
		if strings.Contains(text, "Opening") {
			opened = true
			continue
		}
		if opened {
			if !strings.Contains(text, "font 21") {
				t.Errorf("the first frame laid out on %s shows %q, not its project's font_size", dir, text)
			}
			return
		}
	}
	t.Error("no frame showed the opening cover")
}
