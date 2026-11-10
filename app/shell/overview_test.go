package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var overviewNow = rig.OverviewNow

var gitRepo = rig.GitRepo

func lines(n int, line string) string { return strings.Repeat(line+"\n", n) }

// overviewScene is the window with the directory make makes in home open, and the overview's panels all computed.
func overviewScene(t *testing.T, make func(home string) string) *apptest.Scene {
	t.Helper()
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.WorkDir = make(os.Getenv("HOME")) }})
	s.OverviewOff.Dispose()
	apptest.PluginOf[*shell.Overview](s, "overview").SetClock(func() time.Time { return overviewNow })
	s.Registry().Language(registry.Language{Name: "Go", Extensions: []string{".go"}})
	s.Registry().Language(registry.Language{Name: "SQL", Extensions: []string{".sql"}})
	s.Registry().Language(registry.Language{Name: "YAML", Extensions: []string{".yaml"}})
	s.Registry().Language(registry.Language{Name: "Markdown", Extensions: []string{".md"}})
	s.Until("the overview to finish", func() bool { return overviewDone(s) })
	return s
}

func overviewDone(s *apptest.Scene) bool {
	return apptest.PluginOf[*shell.Overview](s, "overview").Done()
}

// A repo's card against 40-overview: languages, commit activity, contributors and the branch.
func TestOverviewSnapshot(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "src", "usage")
		gitRepo(t, dir, map[string]string{
			"internal/usage/usage.go": lines(610, "\tx := price(plan)"),
			"internal/usage/cache.go": lines(300, "\treturn c.get(k)"),
			"db/schema.sql":           lines(170, "create table t (id int);"),
			"deploy/app.yaml":         lines(90, "key: value"),
			"README.md":               lines(70, "Some words."),
			"Makefile":                lines(60, "all:"),
		}, 60)
		return dir
	})
	text := s.Text()
	for _, want := range []string{"usage", "~/src/usage", "fix/stale-results", "1,300", "Go", "70%", "SQL", "commits in the last year",
		"Change 1", "Liam Galvin", "+ 1 more · 5 people"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overview doesn't show %q:\n%s", want, text)
		}
	}
	s.Golden("overview")
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../../testdata/mockups/40-overview.png")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := snapshot.Compare(mock, img, 0.1)
	t.Logf("overview against 40-overview: %s", d)
	if d.Mean > 0.06 {
		t.Errorf("the overview is %s off 40-overview", d)
	}
}

// A plain directory's card has languages, size and its largest files, and no git panels.
func TestOverviewPlainDirectory(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "notes")
		os.MkdirAll(filepath.Join(dir, "2026"), 0o755)
		os.WriteFile(filepath.Join(dir, "todo.md"), []byte(lines(40, "- a thing")), 0o644)
		os.WriteFile(filepath.Join(dir, "2026", "oct.md"), []byte(lines(12, "words")), 0o644)
		return dir
	})
	text := s.Text()
	for _, want := range []string{"LANGUAGES", "52", "Markdown", "SIZE", "LARGEST FILES", "todo.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overview doesn't show %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"COMMIT ACTIVITY", "CONTRIBUTORS"} {
		if strings.Contains(text, not) {
			t.Errorf("a plain directory shows %q", not)
		}
	}
	s.Golden("overview-plain")
}

// A plugin's panel computes off the loop and shows on the card; closing the last tab brings the overview back.
func TestOverviewPluginPanelAndReturn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.OverviewOff.Dispose()
	s.Registry().Contribute(uireg.OverviewPanel{Name: "todos", Title: "TODOs", Order: 90, Dirs: true, Repos: true,
		Compute: func(registry.Ctx, string) (ui.Node, error) { return ui.TextNode("3 todos"), nil }})
	s.Run("editor.open", registry.Args{"path": "a.txt"})
	s.Frame()
	if strings.Contains(s.Text(), "TODOS") {
		t.Fatal("the overview shows over an open file")
	}
	s.Run("view.close", nil)
	s.Until("the plugin's panel", func() bool { return strings.Contains(s.Text(), "3 todos") })
}

// A Lua panel's rows show on the card.
func TestOverviewLuaPanel(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "proj")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
		return dir
	})
	s.WriteConfig(map[string]string{"init.lua": `oriel.overview.add{ name = "todo", title = "To do", order = 90,
  compute = function(dir) return { { label = "open", value = 3 } } end }`})
	s.ReloadLua()
	s.Until("the Lua panel", func() bool { return strings.Contains(s.Text(), "open\n  3") })
}

// A directory's README shows under its card's panels, as markdown.
func TestOverviewShowsTheReadme(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "pricing")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Pricing\n\nRounds **money** the way finance wants.\n"), 0o644)
		return dir
	})
	s.Until("the README", func() bool { return s.Has("README.MD", "Pricing", "Rounds", "money", "the way finance wants.") })
	if s.Has("**money**") {
		t.Errorf("the README isn't rendered:\n%s", s.Text())
	}
}
