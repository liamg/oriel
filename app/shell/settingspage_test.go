package shell_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Settings keeps the sidebar over a project, and covers it opened from the start view.
func TestSettingsKeepsTheSidebar(t *testing.T) {
	left := func(s *apptest.Scene) float32 {
		t.Helper()
		s.Run("settings.open", nil)
		s.Frame()
		s.Frame()
		return s.NodeRole(render.RoleDialog, "Settings").Bounds.Min.X
	}
	if x := left(apptest.New(t, apptest.Options{})); x != 0 {
		t.Errorf("settings opened from the start view starts at x %v, beside the sidebar", x)
	}
	over := sessionScene(t, core.KindWork, sidebarFixture(t), sidebarSize)
	explorer := over.Node("Explorer").Bounds
	if x := left(over); x < explorer.Max.X {
		t.Errorf("settings over a project starts at x %v, over the sidebar (to %v)", x, explorer.Max.X)
	}
}

func TestOmniboxOverSettings(t *testing.T) {
	s := settingsAt(t, "")
	s.Run("omnibox.open", nil)
	s.Frame()
	s.Frame()
	if m := s.UI().KeyMode(); m != "input" {
		t.Fatalf("with the omnibox over settings, keys go to %q, not the omnibox's input", m)
	}
	s.Type("Esc")
	if omni(s).Open() {
		t.Error("Escape didn't close the omnibox")
	}
	s.Run("omnibox.open", nil)
	s.Frame()
	s.Click(render.Point{X: 8, Y: s.Size().Y - 8}, 0)
	if omni(s).Open() {
		t.Error("a click outside didn't close the omnibox")
	}
}

// settingsAt is the settings page searched for query, drawn.
func settingsAt(t *testing.T, query string) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("settings.open", registry.Args{"query": query})
	s.Frame()
	s.Frame()
	return s
}
