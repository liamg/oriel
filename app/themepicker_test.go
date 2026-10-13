package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

// themeScene has two themes besides the built-in, and a settings file naming oriel.
func themeScene(t *testing.T) (*errScene, string) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	dir := filepath.Join(s.sh.cfg.ConfigDir, "themes")
	os.MkdirAll(dir, 0o755)
	for _, n := range []string{"dawn", "dusk"} {
		p := filepath.Join(dir, n+".toml")
		writeFile(t, p, "name = \""+n+"\"\nextends = \"oriel\"\n")
		s.reg.Theme(theme.File{Path: p})
	}
	settings := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, settings, "[ui]\ntheme = \"oriel\"\n")
	s.settings().loadAll()
	return s, settings
}

func currentTheme(t *testing.T, s *errScene) string {
	v, _, err := s.reg.Options().Get("ui.theme", registry.At{})
	if err != nil {
		t.Fatal(err)
	}
	return v.(string)
}

// A theme picker: the highlighted theme shows at once, Esc goes back to the one before, Enter keeps it.
func TestThemePicker(t *testing.T) {
	s, settings := themeScene(t)
	s.run("Themes", nil)
	s.frame()
	s.frame()
	text := s.frameText()
	for _, want := range []string{"dawn", "dusk", "oriel"} {
		if !strings.Contains(text, want) {
			t.Errorf("the picker lacks %s:\n%s", want, text)
		}
	}
	if got := currentTheme(t, s); got != "oriel" {
		t.Fatalf("opening the picker moved off the current theme: %s", got)
	}
	s.typeKeys("Up") // the list opens on the current theme, oriel, the last
	if got := currentTheme(t, s); got != "dusk" {
		t.Errorf("highlighting dusk shows %s", got)
	}
	s.typeKeys("Up")
	if got := currentTheme(t, s); got != "dawn" {
		t.Errorf("highlighting dawn shows %s", got)
	}
	if got := readFile(t, settings); !strings.Contains(got, `theme = "oriel"`) {
		t.Errorf("previewing wrote the file:\n%s", got)
	}
	s.typeKeys("Esc")
	if got := currentTheme(t, s); got != "oriel" {
		t.Errorf("Esc left %s", got)
	}

	s.run("Themes", nil)
	s.frame()
	s.typeKeys("Up", "CR")
	if got := currentTheme(t, s); got != "dusk" {
		t.Errorf("Enter on dusk left %s", got)
	}
	if got := readFile(t, settings); !strings.Contains(got, `theme = "dusk"`) {
		t.Errorf("Enter didn't keep it:\n%s", got)
	}
}

// ui.theme on the settings page is a menu of the themes there are, not a text field.
func TestThemeOptionIsAMenu(t *testing.T) {
	s, settings := themeScene(t)
	sp, _ := s.reg.Options().Spec("ui.theme")
	if sp.Choices == nil {
		t.Fatal("ui.theme has no choices")
	}
	if got := strings.Join(sp.Choices(), " "); got != "dawn dusk oriel" {
		t.Errorf("choices %q", got)
	}
	s.run("Settings", registry.Args{"args": "ui"})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "oriel ▾") {
		t.Errorf("no menu for ui.theme:\n%s", s.frameText())
	}
	s.settings().menu = "ui.theme"
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "dusk") {
		t.Errorf("the menu lacks dusk:\n%s", s.frameText())
	}
	_ = settings
}

// The theme picker over the editor, highlighting a theme of another colour: the window is already in it.
func TestThemePickerSnapshot(t *testing.T) {
	s := editorScene(t)
	dir := filepath.Join(s.sh.cfg.ConfigDir, "themes")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "glacier.toml")
	writeFile(t, p, "name = \"glacier\"\nextends = \"oriel\"\n\n[dark.palette]\nbg0 = \"#071018\"\nbg1 = \"#0b1620\"\nbg2 = \"#0f1c28\"\naccent = \"#5cc8ff\"\naccent2 = \"#2f9fe0\"\nbrand = \"#7fe3ff\"\n")
	s.reg.Theme(theme.File{Path: p})
	s.size = mockupSize
	s.run("Themes", nil)
	s.frame()
	s.typeKeys("Up")
	srcs, _ := s.reg.Themes()
	r, err := resolveTheme(srcs, currentTheme(t, s), theme.Dark) // as the window's loop does when ui.theme moves
	if err != nil {
		t.Fatal(err)
	}
	s.ui.SetTheme(r)
	s.golden("theme-picker")
}
