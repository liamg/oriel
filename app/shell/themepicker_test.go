package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

// themeScene has two themes besides the built-in, and a settings file naming oriel.
func themeScene(t *testing.T) (*apptest.Scene, string) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	dir := filepath.Join(s.ConfigDir, "themes")
	os.MkdirAll(dir, 0o755)
	for _, n := range []string{"dawn", "dusk"} {
		p := filepath.Join(dir, n+".toml")
		rig.WriteFile(t, p, "name = \""+n+"\"\nextends = \"oriel\"\n")
		s.Registry().Theme(theme.File{Path: p})
	}
	settings := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, settings, "[ui]\ntheme = \"oriel\"\n")
	s.Run("settings.reload", nil)
	return s, settings
}

func currentTheme(t *testing.T, s *apptest.Scene) string {
	v, _, err := s.Registry().Options().Get("ui.theme", registry.At{})
	if err != nil {
		t.Fatal(err)
	}
	return v.(string)
}

// A theme picker: the highlighted theme shows at once, Esc goes back to the one before, Enter keeps it.
func TestThemePicker(t *testing.T) {
	s, settings := themeScene(t)
	s.Run("Themes", nil)
	s.Frame()
	s.Frame()
	text := s.Text()
	for _, want := range []string{"dawn", "dusk", "oriel"} {
		if !strings.Contains(text, want) {
			t.Errorf("the picker lacks %s:\n%s", want, text)
		}
	}
	if got := currentTheme(t, s); got != "oriel" {
		t.Fatalf("opening the picker moved off the current theme: %s", got)
	}
	s.Type("Up") // the list opens on the current theme, oriel, after one-dark
	if got := currentTheme(t, s); got != "one-dark" {
		t.Errorf("highlighting one-dark shows %s", got)
	}
	s.Type("Up")
	if got := currentTheme(t, s); got != "obsidian" {
		t.Errorf("highlighting obsidian shows %s", got)
	}
	if got := rig.ReadFile(t, settings); !strings.Contains(got, `theme = "oriel"`) {
		t.Errorf("previewing wrote the file:\n%s", got)
	}
	s.Type("Esc")
	if got := currentTheme(t, s); got != "oriel" {
		t.Errorf("Esc left %s", got)
	}

	s.Run("Themes", nil)
	s.Frame()
	s.Type("Up", "CR")
	if got := currentTheme(t, s); got != "one-dark" {
		t.Errorf("Enter on one-dark left %s", got)
	}
	if got := rig.ReadFile(t, settings); !strings.Contains(got, `theme = "one-dark"`) {
		t.Errorf("Enter didn't keep it:\n%s", got)
	}
}

// ui.theme on the settings page is a menu of the themes there are, not a text field.
func TestThemeOptionIsAMenu(t *testing.T) {
	s, settings := themeScene(t)
	sp, _ := s.Registry().Options().Spec("ui.theme")
	if sp.Choices == nil {
		t.Fatal("ui.theme has no choices")
	}
	if got := strings.Join(sp.Choices(), " "); got != "abyss aurora bloodthirsty catppuccin dawn dracula dusk ember everforest graphite gruvbox hacker kanagawa neon nord obsidian one-dark oriel plum rose-pine solarized tokyonight verdigris" {
		t.Errorf("choices %q", got)
	}
	s.Run("Settings", registry.Args{"args": "ui"})
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "oriel ▾") {
		t.Errorf("no menu for ui.theme:\n%s", s.Text())
	}
	s.ClickTop("oriel ▾")
	if !strings.Contains(s.Text(), "dusk") {
		t.Errorf("the menu lacks dusk:\n%s", s.Text())
	}
	_ = settings
}

// The theme picker over the editor, highlighting a theme of another colour: the window is already in it.
func TestThemePickerSnapshot(t *testing.T) {
	s := apptest.EditorScene(t)
	dir := filepath.Join(s.ConfigDir, "themes")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "glacier.toml")
	rig.WriteFile(t, p, "name = \"glacier\"\nextends = \"oriel\"\n\n[dark.palette]\nbg0 = \"#071018\"\nbg1 = \"#0b1620\"\nbg2 = \"#0f1c28\"\naccent = \"#5cc8ff\"\naccent2 = \"#2f9fe0\"\nbrand = \"#7fe3ff\"\n")
	s.Registry().Theme(theme.File{Path: p})
	s.Resize(apptest.MockupSize)
	s.Run("Themes", nil)
	s.Frame()
	s.Type("Up", "Up", "Up", "Up", "Up", "Up", "Up", "Up", "Up") // oriel, up past one-dark, obsidian, nord, neon, kanagawa, hacker, gruvbox and graphite
	if got := currentTheme(t, s); got != "glacier" {
		t.Fatalf("highlighting glacier shows %s", got)
	}
	srcs, _ := s.Registry().Themes()
	r, err := shell.ResolveTheme(srcs, currentTheme(t, s), theme.Dark) // as the window's loop does when ui.theme moves
	if err != nil {
		t.Fatal(err)
	}
	s.UI().SetTheme(r)
	s.Golden("theme-picker")
	text := s.Text()
	for _, want := range []string{"sample.go", "PriceFor", "ErrNoTier", "accent", "error"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q", want)
		}
	}
}
