package app_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/app/updates"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

const hiPlugin = `local L = oriel
L.command("Hi", function() L.notify("hi") end)
`

// catalogScene is the app with a data directory of the test's, and the user's settings.toml.
func catalogScene(t *testing.T, workDir string, change ...func(*apptest.Config)) (*apptest.Scene, string) {
	t.Helper()
	data := rig.WritableAfter(t, t.TempDir())
	s := apptest.New(t, apptest.Options{WorkDir: workDir, Config: func(cfg *apptest.Config) {
		cfg.DataDir = data
		for _, f := range change {
			f(cfg)
		}
	}})
	return s, filepath.Join(s.ConfigDir, "settings.toml")
}

func catalogOf(s *apptest.Scene) *settings.Catalog {
	return apptest.PluginOf[*settings.Catalog](s, "catalog")
}

// syncPlugins reads the settings files again and waits for the plugin manager to sync what they say.
func syncPlugins(s *apptest.Scene, t *testing.T) {
	t.Helper()
	settingsPlugin(s).Reload()
	synced := catalogOf(s).Sync()
	if !s.Headless.Until(30*time.Second, func() bool {
		select {
		case <-synced:
			return true
		default:
			return false
		}
	}) {
		t.Fatal("the plugin manager didn't sync")
	}
}

// lazyScene installs acme/hi from a path with lazy in its manifest, and a file to edit.
func lazyScene(t *testing.T, lazy, init string) (*apptest.Scene, string) {
	t.Helper()
	dir := writePlugin(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\nlazy = " + lazy + "\n", "init.lua": init})
	s, settings := catalogScene(t, "")
	rig.WriteFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n\"acme/hi\" = { path = \""+dir+"\" }\n")
	syncPlugins(s, t)
	if hiLoaded(s) {
		t.Fatal("a lazy plugin loaded at start")
	}
	if st := catalogOf(s).State("acme/hi"); st != "waits for first use" {
		t.Fatalf("acme/hi doesn't wait for its first use; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
	file := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(file, []byte("notes\n"), 0o644)
	return s, file
}

func hiLoaded(s *apptest.Scene) bool {
	return slices.ContainsFunc(s.Host().Plugins(), func(m plugin.Manifest) bool { return m.ID == "acme/hi" })
}

func noticed(s *apptest.Scene, text string) int {
	n := 0
	for _, x := range s.Store().State().Notifications {
		if x.Text == text {
			n += x.Count
		}
	}
	return n
}

// A plugin with `lazy = { commands = ["Hi*"] }` isn't loaded until `:Hi` runs, and `:Hi` then works the first
// time.
func TestLazyPluginLoadsOnItsCommand(t *testing.T) {
	s, file := lazyScene(t, `{ commands = ["Hi*"] }`, hiPlugin)
	s.OpenFile(file)
	s.EditorKeys(":Nope<CR>")
	if hiLoaded(s) {
		t.Fatal("a command the glob doesn't match loaded it")
	}
	s.EditorKeys(":Hi<CR>")
	if !hiLoaded(s) {
		t.Fatalf("not loaded by :Hi; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
	if noticed(s, "hi") != 1 {
		t.Errorf(":Hi didn't run the first time; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
	s.EditorKeys(":Hi<CR>")
	if noticed(s, "hi") != 2 {
		t.Errorf(":Hi didn't run again; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
	if st := catalogOf(s).State("acme/hi"); st != "loaded" {
		t.Error("the Plugins page would still say it waits")
	}
}

// The same for an event.
func TestLazyPluginLoadsOnItsEvent(t *testing.T) {
	s, file := lazyScene(t, `{ events = ["BufWritePost"] }`, `oriel.on("BufWritePost", function() oriel.notify("wrote") end)`)
	s.OpenFile(file)
	if hiLoaded(s) {
		t.Fatal("another event loaded it")
	}
	s.EditorKeys(":w<CR>")
	if !hiLoaded(s) || noticed(s, "wrote") != 1 {
		t.Errorf("loaded %v, its hook on the event that loaded it; notices: %v", hiLoaded(s), noticeTexts(s.Store().State().Notifications))
	}
}

// The same for a key.
func TestLazyPluginLoadsOnItsKey(t *testing.T) {
	s, file := lazyScene(t, `{ keys = ["<leader>H"] }`, `oriel.keymap.set({"n"}, "<leader>H", function() oriel.notify("key") end)`)
	s.OpenFile(file)
	pressKeys(s, t, " H")
	if !hiLoaded(s) || noticed(s, "key") != 1 {
		t.Errorf("loaded %v, the key ran; notices: %v", hiLoaded(s), noticeTexts(s.Store().State().Notifications))
	}
	pressKeys(s, t, " H")
	if noticed(s, "key") != 2 {
		t.Errorf("the key didn't run again; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
}

// The same for a filetype: a buffer of one of its filetypes opening.
func TestLazyPluginLoadsOnItsFiletype(t *testing.T) {
	s, file := lazyScene(t, `{ filetypes = ["go"] }`, `oriel.on("BufRead", function() oriel.notify("opened") end)`)
	s.OpenFile(file)
	if hiLoaded(s) {
		t.Fatal("a .txt loaded it")
	}
	gofile := filepath.Join(filepath.Dir(file), "main.go")
	os.WriteFile(gofile, []byte("package main\n"), 0o644)
	s.OpenFile(gofile)
	if !hiLoaded(s) || noticed(s, "opened") != 1 {
		t.Errorf("loaded %v, its hook on the buffer that loaded it; notices: %v", hiLoaded(s), noticeTexts(s.Store().State().Notifications))
	}
}

// :Plugins shows which are loaded and which wait.
func TestPluginsPageShowsLazyWaiting(t *testing.T) {
	s, _ := lazyScene(t, `{ commands = ["Hi*"] }`, hiPlugin)
	s.Run("plugins.open", nil)
	s.Frame()
	s.TypeInto("Filter plugins", "acme")
	s.Frame()
	if !strings.Contains(s.FullText(), "waits for first use") {
		t.Fatalf("the page doesn't say acme/hi waits:\n%s", s.FullText())
	}
	s.Run("Hi", nil)
	s.Frame()
	if strings.Contains(s.FullText(), "waits for first use") {
		t.Errorf("the page still says it waits once loaded:\n%s", s.FullText())
	}
}

// pressKeys types keys through the keymaps as the window does, waiting for the rest of a sequence.
func pressKeys(s *apptest.Scene, t *testing.T, keys string) {
	t.Helper()
	r := registry.NewResolver(s.Registry())
	var acts []registry.Action
	for _, k := range strings.Split(keys, "") {
		acts = append(acts, r.Feed(s.KeyModesFor(k), k, s.Now)...)
	}
	for _, a := range acts {
		if a.Keymap == nil {
			t.Fatalf("%q went unmapped", a.Keys)
		}
		s.Run(a.Keymap.Command, a.Keymap.Args)
	}
	s.Frame()
}

// oriel.placeholder: a Lua plugin manager stands in for what its function provides, as the
// built-in one does.
func TestLuaPlaceholder(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.WriteConfig(map[string]string{"init.lua": `oriel.placeholder("mine", { commands = { "Mine*" } }, function()
  oriel.command("MineNow", function() oriel.notify("mine") end)
end)`})
	s.ReloadLua()
	if x := lua(s); x.Err() != nil {
		t.Fatal(x.Err())
	}
	if !s.Registry().Available("MineNow", nil) || noticed(s, "mine") != 0 {
		t.Fatal("MineNow isn't available, or ran already")
	}
	s.Run("MineNow", nil)
	if noticed(s, "mine") != 1 {
		t.Errorf("MineNow didn't load and run; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
}

func consent(s *apptest.Scene) *settings.Consent {
	return apptest.PluginOf[*settings.Consent](s, "consent")
}

func updatesOf(s *apptest.Scene) *updates.Update {
	return apptest.PluginOf[*updates.Update](s, "update")
}
