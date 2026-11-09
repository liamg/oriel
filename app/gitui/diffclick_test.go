package gitui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// A line pressed in a diff stays the line the click is on, though the diff is made again under the pointer before
// the press lets go: lines come in above it, and another line sits where it was. The click never selects that one.
func TestADiffClickStaysOnItsLineThroughARedraw(t *testing.T) {
	r := gittest.New(t)
	var base strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&base, "line %d\n", i)
	}
	r.Write(map[string]string{"f.txt": base.String()})
	r.Commit("Ada", "base")
	changed := strings.Replace(base.String(), "line 15\n", "line 15, changed\n", 1)
	path := filepath.Join(r.Dir, "f.txt")
	os.WriteFile(path, []byte(changed), 0o644)

	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Run("diff.open", registry.Args{"path": path})
	s.Until("the diff", func() bool { s.RunQueued(); return s.Find("line 15, changed") != nil })
	s.Quiet()
	pressed := s.Find("line 15, changed")
	at := apptest.Centre(pressed.Bounds)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonPrimary})
	s.Frame()

	// Three lines come in just above it: what sat three lines up now sits where it was pressed.
	os.WriteFile(path, []byte(strings.Replace(changed, "line 14\n", "line 14\nnew a\nnew b\nnew c\n", 1)), 0o644)
	s.Run("git.refresh", nil)
	s.Until("the diff made again", func() bool { s.RunQueued(); return s.Find("new a") != nil })
	s.Quiet()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonPrimary})
	s.Frame()
	s.Frame()

	var selected []string
	var walk func(n *render.AccessNode)
	walk = func(n *render.AccessNode) {
		if n.Selected && n.Role == "listitem" {
			selected = append(selected, n.Name)
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(s.UI().AccessTree())
	for _, name := range selected {
		if diffLine := strings.HasPrefix(name, "line ") || strings.HasPrefix(name, "new "); diffLine && name != "line 15, changed" {
			t.Errorf("the click on \"line 15, changed\" selected %q, the line drawn where it was by the time it let go", name)
		}
	}

	now := apptest.Centre(s.Find("line 15, changed").Bounds) // where it is now, pressed and let go there: it's the one selected
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: now})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: now, Buttons: render.ButtonPrimary})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: now, Buttons: render.ButtonPrimary})
	s.Frame()
	s.Frame()
	if n := s.Find("line 15, changed"); n == nil || !n.Selected {
		t.Errorf("a click on \"line 15, changed\" once it had moved didn't select it")
	}
}
