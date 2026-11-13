package app_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

func hostHasCommand(h plugin.Host, id string) bool {
	return slices.ContainsFunc(h.Commands(), func(c registry.Command) bool { return c.ID == id })
}

// A hand edit to settings.toml applies, seen by watching the file rather than a fast poll.
func TestSettingsFileIsWatched(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir()})
	r.WaitText("Open Directory")
	if err := os.WriteFile(filepath.Join(r.ConfigDir, "settings.toml"), []byte("[whichkey]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("whichkey.enabled from the file", func() bool {
		v, l, _ := r.Host.Options().Get("whichkey.enabled", registry.At{})
		return v == false && l == registry.LayerUser
	})
	var fastest time.Duration
	r.Do(func() error {
		if w, ok := kit.WindowOf(r.Host); ok {
			fastest = w.Polls().FastestNow()
		}
		return nil
	})
	if fastest > 0 && fastest < time.Second {
		t.Errorf("a poll runs every %v", fastest)
	}
}

// Saving init.lua loads it again, seen by watching it.
func TestInitLuaIsWatched(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir()})
	r.WaitText("Open Directory")
	conf := r.ConfigDir
	src := `oriel.command("HelloWatched", function() end, { desc = "hi" })`
	if err := os.WriteFile(filepath.Join(conf, "init.lua"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("the command init.lua registers", func() bool {
		return hostHasCommand(r.Host, "HelloWatched")
	})
	if err := os.MkdirAll(filepath.Join(conf, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(conf, "lua", "more.lua"), []byte(`oriel.command("MoreWatched", function() end, {})`), 0o644)
	os.WriteFile(filepath.Join(conf, "init.lua"), []byte(src+"\nrequire(\"more\")\n"), 0o644)
	r.WaitFor("the command lua/more.lua registers", func() bool {
		return hostHasCommand(r.Host, "MoreWatched")
	})
}
