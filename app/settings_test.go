package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func (s *errScene) settings() *settingsExt {
	for _, e := range s.exts {
		if x, ok := e.(*settingsExt); ok {
			return x
		}
	}
	return nil
}

// changed counts the lines a edit removed and added, by longest common subsequence.
func changed(a, b string) (removed, added int) {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return len(x) - lcs[0][0], len(y) - lcs[0][0]
}

const handWritten = `# my settings, by hand

[ui]
# the theme I like
theme = "oriel"   # keep

[whichkey]
enabled = true
`

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// settings.md: "A GUI change touches only the line it changes, so a dotfiles diff is one line", and "Reset removes
// the line from the file". Comments, order and spacing written by hand survive.
func TestSettingsEditOneLine(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, path, handWritten)
	s.settings().loadAll()
	step := func(what string, wantRemoved, wantAdded int, do func()) string {
		t.Helper()
		before := readFile(t, path)
		do()
		after := readFile(t, path)
		if r, a := changed(before, after); r != wantRemoved || a != wantAdded {
			t.Errorf("%s: %d lines removed and %d added, want %d and %d:\n%s", what, r, a, wantRemoved, wantAdded, after)
		}
		return after
	}
	got := step("change a value", 1, 1, func() { s.run("settings.set", registry.Args{"name": "ui.theme", "value": "mine"}) })
	if !strings.Contains(got, "theme = \"mine\"   # keep") || !strings.Contains(got, "# the theme I like") {
		t.Errorf("the line's comment or the one above went:\n%s", got)
	}
	got = step("add a key", 0, 1, func() { s.run("settings.set", registry.Args{"name": "ui.reduced_motion", "value": true}) })
	if !strings.Contains(got, "theme = \"mine\"   # keep\nreduced_motion = true\n") {
		t.Errorf("the new key isn't in its table:\n%s", got)
	}
	got = step("reset", 1, 0, func() { s.run("settings.reset", registry.Args{"name": "ui.theme"}) })
	if strings.Contains(got, "theme =") || !strings.Contains(got, "# the theme I like") {
		t.Errorf("after reset:\n%s", got)
	}
	if v, l, _ := s.reg.Options().Get("ui.theme", registry.At{}); v != "oriel" || l != registry.LayerDefault {
		t.Errorf("after reset ui.theme is %v from %v", v, l)
	}
	// The page's toggle is the same edit.
	s.run("settings.open", registry.Args{"section": "whichkey"})
	s.frame()
	s.frame()
	step("toggle on the page", 1, 1, func() { s.click(centre(s.access("whichkey.enabled").Bounds)) })
	if v, _, _ := s.reg.Options().Get("whichkey.enabled", registry.At{}); v != false {
		t.Errorf("whichkey.enabled is %v", v)
	}
	s.run("settings.undo", nil)
	if got := readFile(t, path); !strings.Contains(got, "enabled = true") {
		t.Errorf("undo:\n%s", got)
	}
}

// settings.md, "Precedence": defaults, the user file, init.lua, the project file, the session. A project file is
// trusted on first open, and again whenever it changes.
func TestSettingsPrecedence(t *testing.T) {
	work := t.TempDir()
	s := newScene(t, "", work, mockupSize)
	s.reg.Option(registry.OptionSpec{Name: "test.level", Type: registry.String, Default: "oriel",
		Scopes: registry.ScopeUser | registry.ScopeProject | registry.ScopeSession})
	o := s.reg.Options()
	user := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	project := filepath.Join(work, ".oriel", "settings.toml")
	here := registry.At{Project: work}
	expect := func(at registry.At, want any, layer registry.Layer, source string) {
		t.Helper()
		v, l, _ := o.Get("test.level", at)
		if v != want || l != layer || o.Source("test.level", at) != source {
			t.Errorf("test.level = %v from %v (%q), want %v from %v (%q)", v, l, o.Source("test.level", at), want, layer, source)
		}
	}
	writeFile(t, user, "[test]\nlevel = \"from-user\"\n")
	s.settings().loadAll()
	expect(here, "from-user", registry.LayerUser, user)

	o.SetFrom("test.level", registry.LayerLua, registry.At{}, "from-lua", "init.lua:12")
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")

	writeFile(t, project, "[test]\nlevel = \"from-project\"\n")
	s.settings().loadAll()
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")
	n := s.store.State().Notifications
	if len(n) == 0 || n[len(n)-1].ID != "settings.untrusted" || len(n[len(n)-1].Actions) == 0 || n[len(n)-1].Actions[0].Command != "settings.trust" {
		t.Fatalf("no notice offering to trust the project's settings: %+v", n)
	}
	s.run("settings.trust", registry.Args{"path": project})
	expect(here, "from-project", registry.LayerProject, project)
	expect(registry.At{}, "from-lua", registry.LayerLua, "init.lua:12")

	writeFile(t, project, "[test]\nlevel = \"changed\"\n")
	s.settings().loadAll()
	expect(here, "from-lua", registry.LayerLua, "init.lua:12")

	o.Set("test.level", registry.LayerSession, registry.At{Session: "s1"}, "from-session")
	expect(registry.At{Project: work, Session: "s1"}, "from-session", registry.LayerSession, "")

	o.Unset("test.level", registry.LayerLua, registry.At{})
	writeFile(t, user, "[test]\n")
	s.settings().loadAll()
	expect(registry.At{}, "oriel", registry.LayerDefault, "")
}

// settings.md: "A value set in Lua is read-only on the page, with a jump to the line."
func TestSettingsLuaIsReadOnly(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, path, handWritten)
	s.settings().loadAll()
	s.reg.Options().SetFrom("whichkey.enabled", registry.LayerLua, registry.At{}, false, "init.lua:12")
	err := s.reg.Run(context.Background(), "settings.set", registry.Args{"name": "whichkey.enabled", "value": true})
	if err == nil || !strings.Contains(err.Error(), "init.lua:12") {
		t.Errorf("setting a Lua-set option: %v", err)
	}
	if got := readFile(t, path); got != handWritten {
		t.Errorf("the file changed:\n%s", got)
	}
	s.run("settings.open", registry.Args{"section": "whichkey"})
	s.frame()
	s.frame()
	if n := s.access("whichkey.enabled"); !n.Disabled {
		t.Error("the toggle isn't disabled")
	}
	text := s.frameText()
	for _, want := range []string{"init.lua:12", "init.lua wins over settings.toml", "Open init.lua:12"} {
		if !strings.Contains(text, want) {
			t.Errorf("the row lacks %q:\n%s", want, text)
		}
	}
	if s.access("Open init.lua:12") == nil {
		t.Error("no link to the line")
	}
}

// settings.md, "Precedence": the project file and the session come after init.lua, so at those scopes a Lua-set
// option is editable, and what's set there wins.
func TestSettingsLuaIsOverriddenAboveIt(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.settings().loadAll()
	s.reg.Options().SetFrom("editor.expand_tab", registry.LayerLua, registry.At{}, false, "init.lua:12")
	if err := s.reg.Run(context.Background(), "settings.set", registry.Args{"name": "editor.expand_tab", "value": true, "scope": "session"}); err != nil {
		t.Fatalf("setting it for the session: %v", err)
	}
	if v, l, _ := s.reg.Options().Get("editor.expand_tab", s.host.Here()); v != true || l != registry.LayerSession {
		t.Errorf("editor.expand_tab = %v from %s", v, l)
	}
	s.run("settings.open", registry.Args{"section": "editor"})
	s.settings().scope = 2
	s.frame()
	s.frame()
	if n := s.access("editor.expand_tab"); n.Disabled {
		t.Error("the toggle is disabled at session scope")
	}
}

// settings.md, "Live reload": a parse error keeps the last good values and says where, and nothing is written back.
func TestSettingsParseError(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, path, "[ui]\ntheme = \"good\"\n")
	s.settings().loadAll()
	writeFile(t, path, "[ui]\ntheme = = \"bad\"\n")
	s.settings().loadAll()
	if v, _, _ := s.reg.Options().Get("ui.theme", registry.At{}); v != "good" {
		t.Errorf("ui.theme = %v", v)
	}
	n := s.store.State().Notifications
	if len(n) == 0 || !strings.Contains(n[len(n)-1].Text, "2:9") {
		t.Errorf("no notice with the line and column: %+v", n)
	}
	if err := s.reg.Run(context.Background(), "settings.set", registry.Args{"name": "ui.theme", "value": "x"}); err == nil {
		t.Error("wrote to a file that doesn't parse")
	}
	if got := readFile(t, path); got != "[ui]\ntheme = = \"bad\"\n" {
		t.Errorf("the file changed: %q", got)
	}
}

// The page, against a golden.
func TestSettingsSnapshot(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), handWritten)
	s.settings().loadAll()
	s.reg.Options().SetFrom("ui.reduced_motion", registry.LayerLua, registry.At{}, true, "init.lua:12")
	s.run("Settings", registry.Args{"args": "ui"})
	s.golden("settings")
}

// settingsAt is the settings page searched for query, drawn.
func settingsAt(t *testing.T, query string) *errScene {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.run("settings.open", registry.Args{"query": query})
	s.frame()
	s.frame()
	return s
}

func TestSettingsInputIsAMenuOfEngines(t *testing.T) {
	s := settingsAt(t, "editor.input")
	s.clickText(t, "vim ▾")
	s.clickText(t, "plain")
	if v, _, _ := s.reg.Options().Get(InputOption, registry.At{}); v != "plain" {
		t.Errorf("picking plain from editor.input's menu: %v", v)
	}
}

func TestSettingsEscapeAfterAToggle(t *testing.T) {
	s := settingsAt(t, "editor.expand_tab")
	s.click(centre(s.access("editor.expand_tab").Bounds))
	if !s.settings().open {
		t.Fatal("clicking a toggle closed the page")
	}
	s.typeKeys("Esc")
	s.frame()
	if s.settings().open {
		t.Error("Escape with a toggle focused didn't close the settings page")
	}
}

func TestSettingsNumberField(t *testing.T) {
	s := settingsAt(t, "editor.max_line_length")
	s.click(centre(s.access("editor.max_line_length").Bounds))
	s.typeKeys("End", "1", "x", "2")
	if v := s.access("editor.max_line_length").Value; v != "012" {
		t.Errorf("typing 1 x 2 into a whole number's field: %q, want 012", v)
	}
	s.typeKeys("CR")
	if v, _, _ := s.reg.Options().Get("editor.max_line_length", registry.At{}); v != 12 {
		t.Errorf("after Enter, editor.max_line_length = %v", v)
	}
	s.typeKeys("0", "0", "0", "0")
	s.frame()
	if !strings.Contains(s.frameText(), "is between") {
		t.Error("an out-of-range value doesn't say why under the field")
	}
	s.typeKeys("CR")
	if v, _, _ := s.reg.Options().Get("editor.max_line_length", registry.At{}); v != 12 {
		t.Errorf("Enter on an out-of-range value set it: %v", v)
	}
	s.typeKeys("Esc")
	s.frame()
	if v := s.access("editor.max_line_length").Value; v != "12" {
		t.Errorf("Escape leaves %q in the field, want it back to 12", v)
	}
}

func TestOmniboxOverSettings(t *testing.T) {
	s := settingsAt(t, "")
	s.run("omnibox.open", nil)
	s.frame()
	s.frame()
	if m := s.ui.KeyMode(); m != "input" {
		t.Fatalf("with the omnibox over settings, keys go to %q, not the omnibox's input", m)
	}
	s.typeKeys("Esc")
	if s.omni().open {
		t.Error("Escape didn't close the omnibox")
	}
	s.run("omnibox.open", nil)
	s.frame()
	s.click(render.Point{X: 8, Y: s.size.Y - 8})
	if s.omni().open {
		t.Error("a click outside didn't close the omnibox")
	}
}
