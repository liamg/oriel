package gitui_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// A diff in a tab behind another comes back where it was scrolled to, not at the top.
func TestTheDiffKeepsItsScrollBehindAnotherTab(t *testing.T) {
	r := gittest.New(t)
	before, after := map[string]string{}, map[string]string{}
	for i := range 40 {
		name := fmt.Sprintf("f%02d.txt", i)
		before[name], after[name] = "one\n", "one\ntwo\n"
	}
	r.Write(before)
	r.Commit("Ada", "base")
	r.Write(after)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "f00.txt") })
	s.Quiet()
	list := s.FindRole(render.RoleList, "Changes")
	if list == nil {
		t.Fatalf("no diff list:\n%s", s.Text())
	}
	at := apptest.Centre(list.Bounds)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: 600}})
	s.Frames(3)
	scrolled := s.UI().Scroll(ui.ID(list.ID)).Y
	if scrolled <= 0 {
		t.Fatal("the wheel didn't scroll the diff")
	}

	s.Run("editor.open", registry.Args{"path": "f00.txt"})
	s.Frames(3)
	if s.FindRole(render.RoleList, "Changes") != nil {
		t.Fatal("the diff still shows with the file's tab in front")
	}
	var diff layout.ViewRef
	for _, v := range s.Host().Layout().All() {
		if v.ID == kit.DiffView {
			diff = v
		}
	}
	s.Host().Layout().Show(diff)
	s.Frames(3)
	list = s.FindRole(render.RoleList, "Changes")
	if list == nil {
		t.Fatalf("the diff's tab didn't come back:\n%s", s.Text())
	}
	if got := s.UI().Scroll(ui.ID(list.ID)).Y; got != scrolled {
		t.Errorf("back in front, the diff is scrolled to %v, want %v where it was", got, scrolled)
	}
}
