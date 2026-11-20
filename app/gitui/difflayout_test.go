package gitui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// docs/configuration.md: "diff.layout: split: the review diff's old and new side by side; unified, one above the
// other". Set in settings.toml, the diff opens in it.
func TestTheDiffLayoutSettingIsRespected(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"notes.txt": "first\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"notes.txt": "first\nsecond\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.WriteConfig(map[string]string{"settings.toml": "[diff]\nlayout = \"unified\"\n"})
	s.Run("settings.reload", nil)
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "second") })
	s.Quiet()
	if v, _ := registry.Option[string](s.Registry().Options(), "diff.layout", registry.At{}); v != "unified" {
		t.Errorf("diff.layout reads %q", v)
	}
	if n := s.FindRole(render.RoleTab, "unified"); n == nil || !n.Selected {
		t.Errorf("the diff isn't unified: %+v\n%s", n, s.Text())
	}
}

// help files/diff#layout: "The choice is remembered (diff.layout)": picked in the diff, it's kept in settings.toml,
// as the settings page keeps it, and the diff opens in it after a restart.
func TestTheDiffsLayoutChoiceLastsARestart(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"notes.txt": "first\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"notes.txt": "first\nsecond\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "second") })
	s.Quiet()
	s.Click(apptest.Centre(s.FindRole(render.RoleTab, "unified").Bounds), 0)
	s.Quiet()
	kept, err := os.ReadFile(filepath.Join(s.ConfigDir, "settings.toml"))
	if err != nil || !strings.Contains(string(kept), "[diff]\nlayout = \"unified\"") {
		t.Fatalf("the choice isn't in settings.toml: %q, %v", kept, err)
	}

	again := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	again.WriteConfig(map[string]string{"settings.toml": string(kept)}) // the next start's settings: the file the first kept
	again.Run("settings.reload", nil)
	again.Quiet()
	again.Run("diff.open", registry.Args{"path": r.Dir})
	again.Until("the diff drawn after the restart", func() bool { return strings.Contains(again.Text(), "second") })
	again.Quiet()
	if n := again.FindRole(render.RoleTab, "unified"); n == nil || !n.Selected {
		t.Errorf("after a restart the diff isn't unified: %+v", n)
	}
}
