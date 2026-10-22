package app_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/voice/voicetest"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
)

// The built-ins not needed at start, lazy through their manifests as any plugin's: stats' page, the theme editor,
// voice, debug and tests.
var lazyBuiltins = []string{"stats.page", "theme", "voice", "debug", "tests"}

func loaded(s *apptest.Scene, id string) bool {
	return slices.ContainsFunc(s.Host().Plugins(), func(m plugin.Manifest) bool { return m.ID == id })
}

// Built-ins that aren't needed at start go lazy: starting loads none of them, and :Plugins says each waits.
func TestLazyBuiltinsWaitAtStart(t *testing.T) {
	s, _ := catalogScene(t, t.TempDir())
	syncPlugins(s, t)
	for _, id := range lazyBuiltins {
		if loaded(s, id) {
			t.Errorf("%s loaded at start", id)
		}
		if st := catalogOf(s).State(id); st != "waits for first use" {
			t.Errorf("the Plugins page says %s is %q", id, st)
		}
	}
	s.Frame()
	for _, id := range lazyBuiltins {
		if loaded(s, id) {
			t.Errorf("%s loaded by drawing the window", id)
		}
	}
}

// Each trigger loads its plugin, and then the trigger goes on as if it had always been loaded.
func TestLazyBuiltinsLoadOnTheirTriggers(t *testing.T) {
	t.Run("stats page by its key", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.Press("<D-S>")
		s.Frame()
		if !loaded(s, "stats.page") || !placeShown(s, "stats") {
			t.Errorf("loaded %v; the page isn't open", loaded(s, "stats.page"))
		}
	})
	t.Run("theme editor by its command", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.Run("theme.edit", nil)
		s.Frame()
		if !loaded(s, "theme") || !placeShown(s, "theme") {
			t.Errorf("loaded %v; the editor isn't open", loaded(s, "theme"))
		}
	})
	t.Run("voice by a field's mic", func(t *testing.T) {
		mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(600*time.Millisecond),
			voicetest.Silence(700*time.Millisecond))}
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Config: func(c *apptest.Config) {
			c.Voice = agentui.VoiceConfig{Mic: mic, Transcriber: &voicetest.Transcriber{}, Whisper: c.Voice.Whisper}
		}})
		s.Run("prompts.open", map[string]any{"id": prompts.StyleID})
		s.Frame()
		s.Frame()
		if loaded(s, "voice") {
			t.Fatal("a long field loaded voice")
		}
		s.ClickNode("Dictate")
		if !loaded(s, "voice") || !apptest.PluginOf[*agentui.Voice](s, "voice").Dictating() {
			t.Errorf("loaded %v; the mic didn't start dictating", loaded(s, "voice"))
		}
	})
	t.Run("debug by its rail entry's panel", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		c, ok := s.Registry().Contribution("rail.entry", "debug")
		if !ok {
			t.Fatal("no Debug rail entry before debug loads")
		}
		s.Run(c.(uireg.RailEntry).Command, c.(uireg.RailEntry).Args)
		s.Frame()
		if !loaded(s, "debug") || !kit.Showing(s.Host(), lang.DebugView, "") {
			t.Errorf("loaded %v; the Debug panel isn't showing", loaded(s, "debug"))
		}
	})
	t.Run("debug by a file it debugs", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.OpenFile(writeFile(t, "notes.txt", "notes\n"))
		if loaded(s, "debug") {
			t.Fatal("a .txt loaded debug")
		}
		s.OpenFile(writeFile(t, "main.go", "package main\n"))
		if !loaded(s, "debug") {
			t.Error("a .go file didn't load debug")
		}
	})
	t.Run("tests by a test file", func(t *testing.T) {
		dir := t.TempDir()
		s := apptest.New(t, apptest.Options{WorkDir: dir})
		rig.WriteFiles(t, dir, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "main.go": "package main\n",
			"main_test.go": "package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"})
		s.OpenFile(filepath.Join(dir, "main.go"))
		if loaded(s, "tests") {
			t.Fatal("a file with no tests loaded tests")
		}
		s.OpenFile(filepath.Join(dir, "main_test.go"))
		s.WaitSyntax()
		s.Frame()
		ds := s.Decorations(0, s.EditorWindow().Text.Snapshot().Lines())[4]
		if !loaded(s, "tests") || !slices.ContainsFunc(ds, func(d uireg.Decoration) bool { return d.Text == "▶" }) {
			t.Errorf("loaded %v; no ▶ on the test: %+v", loaded(s, "tests"), ds)
		}
	})
	t.Run("tests by its key", func(t *testing.T) {
		s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
		s.OpenFile(writeFile(t, "notes.txt", "notes\n"))
		pressKeys(s, t, " ts")
		if !loaded(s, "tests") || !kit.Showing(s.Host(), lang.TestsView, "") {
			t.Errorf("loaded %v; the Tests panel isn't showing", loaded(s, "tests"))
		}
	})
}

// placeShown says the page that's the window's place name is open.
func placeShown(s *apptest.Scene, name string) bool {
	c, ok := s.Registry().Contribution("place", name)
	return ok && c.(uireg.Place).Shown()
}

// Bundled plugins load at start with what their manifest declares: a bundled plugin's own lazy keeps it waiting
// until its first use.
func TestBundledPluginReadsItsLazy(t *testing.T) {
	hi := plugin.Bundled{FS: fstest.MapFS{
		"oriel.toml": {Data: []byte("id = \"acme/hi\"\nversion = \"1.0.0\"\nlazy = { commands = [\"Hi*\"] }\n")},
		"init.lua":   {Data: []byte(hiPlugin)},
	}}
	s, _ := catalogScene(t, t.TempDir(), func(cfg *apptest.Config) { cfg.Bundled = []plugin.Bundled{hi} })
	if st := catalogOf(s).State("acme/hi"); st != "waits for first use" || hiLoaded(s) {
		t.Fatalf("the bundled plugin is %q at start", st)
	}
	s.Run("Hi", nil)
	if st := catalogOf(s).State("acme/hi"); st != "loaded" || noticed(s, "hi") != 1 {
		t.Errorf("after :Hi it's %q; notices: %v", st, noticeTexts(s.Store().State().Notifications))
	}
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
