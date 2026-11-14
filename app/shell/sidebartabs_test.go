package shell_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// A file from another project opens in the one tab strip, led by its project's colour, its tooltip and
// breadcrumb naming the project; the sidebar stays where it was.
func TestSidebarTabsAcrossProjects(t *testing.T) {
	ps := apptest.SidebarProjects(t)
	s := apptest.SessionScene(t, core.KindWork, ps, apptest.MockupSize)
	s.Run("editor.open", registry.Args{"path": filepath.Join(ps[0].Dir, "src/components/FilterBar.tsx")})
	s.Run("editor.open", registry.Args{"path": filepath.Join(ps[1].Dir, "proto/usage.proto")})
	s.Frame()
	if got := apptest.OpenProject(s); got != "admin-ui" {
		t.Errorf("opening api's file moved the sidebar to %q", got)
	}
	var tabs []layout.ViewRef
	for _, v := range s.Host().Layout().Shown() {
		tabs = append(tabs, v)
	}
	leaf := s.Host().Layout().Find(tabs[0]).Leaf
	if len(leaf.Tabs) != 2 {
		t.Fatalf("one tab strip: %v", leaf.Tabs)
	}
	violet := kit.TokenColour(s.UI().Theme(), "violet", render.Color{})
	if c, ok := shell.PaneLead(s.Headless, leaf.Tabs[1]); !ok || c != violet {
		t.Errorf("usage.proto's tab leads with %v %v, want api's violet", c, ok)
	}
	if tip := shell.PaneTip(s.Headless, leaf.Tabs[1]); !strings.Contains(tip, "api") || !strings.Contains(tip, "proto/usage.proto") {
		t.Errorf("tooltip %q", tip)
	}
	if !strings.Contains(s.Text(), "api\n›\nproto\n›\nusage.proto") {
		t.Errorf("the breadcrumb doesn't start with the project:\n%s", s.Text())
	}
}
