package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// tabDragScene is a.go and b.go open as tabs in main, and the agent console in the bottom dock.
func tabDragScene(t *testing.T) *apptest.Scene {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Alpha() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n\nfunc Bravo() {}\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": "a.go"})
	s.Run("editor.open", registry.Args{"path": "b.go"})
	s.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "dock", "edge": "bottom", "title": "Agent"})
	s.Frames(3)
	return s
}

// tabPoint is the middle of name's tab, a mark after its name or not, or of its dock's header when it's alone there.
func tabPoint(s *apptest.Scene, name string) render.Point {
	s.T.Helper()
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == render.RoleTab && (n.Name == name || strings.HasPrefix(n.Name, name+" ")) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	if n := find(s.Access()); n != nil {
		return apptest.Centre(n.Bounds)
	}
	return s.TextPoint(strings.ToUpper(name))
}

func region(s *apptest.Scene, name string) render.Rect {
	s.T.Helper()
	return s.NodeRole(render.RoleRegion, name).Bounds
}

// dragTab drags name's tab to the point at gives, from the frame as it is when the drag starts.
func dragTab(name string, at func(s *apptest.Scene) render.Point) func(s *apptest.Scene) {
	return func(s *apptest.Scene) {
		s.T.Helper()
		s.Drag(tabPoint(s, name), at(s))
		s.Frames(2)
	}
}

func centreOf(name string) func(s *apptest.Scene) render.Point {
	return func(s *apptest.Scene) render.Point { return apptest.Centre(region(s, name)) }
}

// mainAt is a point in main, fx and fy of the way across and down.
func mainAt(fx, fy float32) func(s *apptest.Scene) render.Point {
	return func(s *apptest.Scene) render.Point {
		m := region(s, "Main")
		return render.Point{X: m.Min.X + m.Dx()*fx, Y: m.Min.Y + 30 + (m.Dy()-30)*fy}
	}
}

// typesInto says keys typed now reach a.go's window: vim's iZ<Esc> puts a Z at its start.
func typesInto(s *apptest.Scene) {
	s.T.Helper()
	for _, k := range []string{"i", "Z", "<Esc>"} {
		s.Press(k)
		s.Frames(1)
	}
	w, ok := s.Editor().Window("1")
	if !ok || !strings.HasPrefix(w.Text.Snapshot().String(), "Zpackage a") {
		s.T.Errorf("keys didn't reach a.go:\n%s", s.Host().Layout().Dump())
	}
}

// A tab dragged anywhere a drop lights up lands there with its content intact and takes the keys: an editor tab
// keeps its window and file wherever it goes, and back again.
func TestTabDragKeepsTheView(t *testing.T) {
	window := func(s *apptest.Scene) render.Point { return render.Point{X: s.Size().X, Y: s.Size().Y} }
	toDock := dragTab("a.go", centreOf("Bottom dock"))
	flows := []struct {
		name  string
		steps []func(s *apptest.Scene)
		want  string // the layout's dump holds this
	}{
		{"onto the agent console's dock", []func(*apptest.Scene){toDock}, "dock bottom open=true [agent.console *editor#1]"},
		{"onto the dock and back into main", []func(*apptest.Scene){toDock, dragTab("a.go", mainAt(.5, .5))}, "leaf [editor#2 *editor#1]"},
		{"onto the right window edge", []func(*apptest.Scene){dragTab("a.go", func(s *apptest.Scene) render.Point {
			return render.Point{X: window(s).X - 4, Y: window(s).Y / 2}
		})}, "dock right open=true [*editor#1]"},
		{"onto the left window edge, then the bottom dock", []func(*apptest.Scene){
			dragTab("a.go", func(s *apptest.Scene) render.Point { return render.Point{X: 4, Y: window(s).Y / 2} }), toDock},
			"dock bottom open=true [agent.console *editor#1]"},
		{"split right", []func(*apptest.Scene){dragTab("a.go", mainAt(.9, .5))}, "split h [1 1]"},
		{"split below, then back into the other's tabs", []func(*apptest.Scene){
			dragTab("a.go", mainAt(.5, .9)), dragTab("a.go", mainAt(.5, .2))},
			"leaf [editor#2 *editor#1]"},
		{"into a float, then the bottom dock", []func(*apptest.Scene){
			dragTab("a.go", func(s *apptest.Scene) render.Point { return render.Point{X: 600, Y: 10} }), toDock},
			"dock bottom open=true [agent.console *editor#1]"},
		{"main emptied into the dock, then filled again", []func(*apptest.Scene){
			dragTab("b.go", centreOf("Bottom dock")), toDock, dragTab("a.go", mainAt(.5, .5))},
			"leaf [*editor#1]"},
		{"the console split beside it, then a.go clicked", []func(*apptest.Scene){
			dragTab("Agent", mainAt(.9, .5)), func(s *apptest.Scene) { s.Click(tabPoint(s, "a.go"), 0); s.Frames(2) }},
			"leaf [*agent.console]"},
		{"split right, then onto the other's tab strip", []func(*apptest.Scene){
			dragTab("a.go", mainAt(.9, .5)), dragTab("a.go", func(s *apptest.Scene) render.Point { return tabPoint(s, "b.go") })},
			"leaf [editor#2 *editor#1]"},
		{"onto a float's tab strip", []func(*apptest.Scene){
			dragTab("b.go", func(s *apptest.Scene) render.Point { return render.Point{X: 600, Y: 10} }),
			dragTab("a.go", func(s *apptest.Scene) render.Point { return tabPoint(s, "b.go") })},
			"[editor#2 *editor#1]\n"},
		{"onto its own leaf's middle: stays", []func(*apptest.Scene){dragTab("a.go", mainAt(.5, .5))}, "leaf [*editor#1 editor#2]"},
	}
	for _, f := range flows {
		t.Run(f.name, func(t *testing.T) {
			s := tabDragScene(t)
			s.Run("editor.open", registry.Args{"path": "a.go"})
			s.Frames(2)
			for _, step := range f.steps {
				step(s)
			}
			dump := s.Host().Layout().Dump()
			if !strings.Contains(dump, f.want) {
				t.Errorf("the layout lacks %q:\n%s", f.want, dump)
			}
			if w, ok := s.Editor().Window("1"); !ok || filepath.Base(w.Path) != "a.go" {
				t.Fatalf("a.go's window is gone: %+v\n%s", w, dump)
			}
			text := s.FullText()
			if strings.Contains(text, "this window is closed") || !strings.Contains(text, "Alpha") {
				t.Errorf("a.go's tab doesn't show it:\n%s\n%s", dump, text)
			}
			typesInto(s)
		})
	}
}

// ⌃W's moves keep the keys in the view they move.
func TestKeyMovesKeepTheKeys(t *testing.T) {
	for _, k := range []string{"<C-w>J", "<C-w>L", "<C-w>F", "<C-w>T", "<C-`>"} {
		t.Run(k, func(t *testing.T) {
			s := tabDragScene(t)
			s.Run("editor.open", registry.Args{"path": "a.go"})
			s.Frames(2)
			s.Press(k)
			s.Frames(2)
			typesInto(s)
		})
	}
}

// holdTab presses name's tab and holds it over the point at gives, without letting go.
func holdTab(s *apptest.Scene, name string, at func(s *apptest.Scene) render.Point) {
	s.T.Helper()
	from, to := tabPoint(s, name), at(s)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.Frame()
	for _, p := range []render.Point{{X: from.X + 10, Y: from.Y + 10}, to, to} {
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		s.Frame()
	}
}

// Held over another pane's tab strip, a tab shows as one more tab there, not as a split.
func TestTabHeldOverATabStripShowsAsATab(t *testing.T) {
	s := tabDragScene(t)
	s.Run("editor.open", registry.Args{"path": "a.go"})
	s.Frames(2)
	dragTab("a.go", mainAt(.9, .5))(s)
	holdTab(s, "a.go", func(s *apptest.Scene) render.Point { return tabPoint(s, "b.go") })
	if text := s.Text(); !strings.Contains(text, "Tab with b.go") || strings.Contains(text, "Split") {
		t.Errorf("the drop isn't shown as a tab:\n%s", text)
	}
}

// Held over a dock with views in it, a tab shows as one more tab there, not as the dock taken over.
func TestTabHeldOverADockShowsAsATab(t *testing.T) {
	s := tabDragScene(t)
	holdTab(s, "a.go", centreOf("Bottom dock"))
	if text := s.Text(); !strings.Contains(text, "Tab with Agent") || strings.Contains(text, "Dock bottom") {
		t.Errorf("the drop isn't shown as a tab:\n%s", text)
	}
}
