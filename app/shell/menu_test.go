package shell_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// rightClick right-clicks at p and draws the menu.
func rightClick(s *apptest.Scene, p render.Point) string {
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonSecondary})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonSecondary})
	s.Frame()
	s.Frame()
	return s.Text()
}

// menuScene has a Go file and a Markdown file open side by side, Problems docked, and a Lua plugin's entry for Go
// files.
func menuScene(t *testing.T) (*apptest.Scene, map[string]render.Rect) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.WriteConfig(map[string]string{"init.lua": `oriel.menu.add{ label = "Run go vet", group = "go", when = { file = "*.go" },
  run = function(at) oriel.notify("vet " .. at.file) end }
`})
	s.ReloadLua()
	s.Run("editor.open", registry.Args{"path": "main.go"})
	s.Run("editor.open", registry.Args{"path": "README.md", "place": "vsplit"})
	s.Run("Problems", nil)
	s.Frame()
	s.Frame()
	rects := map[string]render.Rect{}
	for _, v := range s.Host().Layout().Visible() {
		if r, ok := s.Host().PaneRect(v); ok {
			name := v.ID
			if f, _ := editorFile(s, v.Instance); f != "" {
				name = filepath.Base(f)
			}
			rects[name] = r
		}
	}
	return s, rects
}

func editorFile(s *apptest.Scene, instance string) (string, bool) {
	v, ok := s.Registry().ViewByID(kit.EditorView)
	if !ok {
		return "", false
	}
	l, ok := uireg.As[registry.Located](v)
	if !ok {
		return "", false
	}
	return l.Location(instance)
}

// A Lua plugin adds an entry to the editor's right-click menu for `*.go` files only; it shows there, not in
// a `.md` file or the Problems view, and runs its function.
func TestPluginMenuEntry(t *testing.T) {
	s, rects := menuScene(t)
	for _, want := range []string{"main.go", "README.md", "problems"} {
		if _, ok := rects[want]; !ok {
			t.Fatalf("no %s pane: %v", want, rects)
		}
	}
	if text := s.RightClick(apptest.Centre(rects["README.md"])); strings.Contains(text, "Run go vet") || !strings.Contains(text, "? Help") {
		t.Errorf("the Markdown file's menu:\n%s", text)
	}
	s.Type("Esc")
	if text := s.RightClick(apptest.Centre(rects["problems"])); strings.Contains(text, "Run go vet") {
		t.Errorf("Problems' menu has the Go entry:\n%s", text)
	}
	s.Type("Esc")
	text := s.RightClick(apptest.Centre(rects["main.go"]))
	if !strings.Contains(text, "Run go vet") {
		t.Fatalf("the Go file's menu lacks the entry:\n%s", text)
	}
	if strings.Index(text, "Run go vet") > strings.Index(text, "? Help") {
		t.Errorf("Help isn't last:\n%s", text)
	}
	b := s.Node("Run go vet")
	if b == nil {
		t.Fatal("no menu item to click")
	}
	s.Click(apptest.Centre(b.Bounds), 0)
	if n, _ := apptest.LastNotice(s.Store().State(), "lua."); !strings.HasSuffix(n.Text, "main.go") || !strings.HasPrefix(n.Text, "vet ") {
		t.Errorf("the entry didn't run: %+v", n)
	}
}

// The built-in entries come from the same contribution point; entries keep their group order, with Help last, and a
// disposed plugin's entries go.
func TestBuiltinMenuEntries(t *testing.T) {
	s, rects := menuScene(t)
	var names []string
	for _, c := range s.Registry().Contributions("menu.item") {
		names = append(names, c.ContributionName())
	}
	for _, want := range []string{"editor.copy-path", "pane.move", "pane.close"} {
		if !slices.Contains(names, want) {
			t.Errorf("no built-in %s in %v", want, names)
		}
	}
	text := s.RightClick(apptest.Centre(rects["main.go"]))
	order := []string{"Copy path", "Run go vet", "Move pane", "Close", "? Help"}
	last := -1
	for _, w := range order {
		i := strings.Index(text, w)
		if i < 0 || i < last {
			t.Fatalf("menu order wants %v:\n%s", order, text)
		}
		last = i
	}
	s.Type("Esc")
	s.WriteConfig(map[string]string{"init.lua": ""})
	s.ReloadLua()
	if text := s.RightClick(apptest.Centre(rects["main.go"])); strings.Contains(text, "Run go vet") {
		t.Errorf("the reloaded-away entry is still there:\n%s", text)
	}
}

// A tab's right-click menu closes it, the others in its pane, those to its left or right, or all of them.
func TestTabMenuCloses(t *testing.T) {
	cases := []struct {
		item string
		left []string
	}{
		{"Close", []string{"a.go", "c.go", "d.go"}},
		{"Close Others", []string{"b.go"}},
		{"Close to the Left", []string{"b.go", "c.go", "d.go"}},
		{"Close to the Right", []string{"a.go", "b.go"}},
		{"Close All", nil},
	}
	for _, tc := range cases {
		t.Run(tc.item, func(t *testing.T) {
			dir := t.TempDir()
			s := apptest.New(t, apptest.Options{WorkDir: dir})
			for _, f := range []string{"a.go", "b.go", "c.go", "d.go"} {
				os.WriteFile(filepath.Join(dir, f), []byte("package main\n"), 0o644)
				s.Run("editor.open", registry.Args{"path": f})
			}
			s.Frame()
			s.Frame()
			tab := tabNamed(s, "b.go") // not the one in front
			if tab == nil {
				t.Fatalf("no b.go tab:\n%s", s.Text())
			}
			text := s.RightClick(apptest.Centre(tab.Bounds))
			order := []string{"Close", "Close Others", "Close to the Left", "Close to the Right", "Close All", "? Help"}
			last := -1
			for _, w := range order {
				i := strings.Index(text[last+1:], w)
				if i < 0 {
					t.Fatalf("the tab's menu wants %v:\n%s", order, text)
				}
				last += 1 + i
			}
			b := s.Node(tc.item)
			if b == nil {
				t.Fatalf("no %q to click", tc.item)
			}
			s.Click(apptest.Centre(b.Bounds), 0)
			s.Frame()
			var open []string
			for _, v := range s.Host().Layout().All() {
				if f, _ := editorFile(s, v.Instance); v.ID == kit.EditorView && f != "" {
					open = append(open, filepath.Base(f))
				}
			}
			if !slices.Equal(open, tc.left) {
				t.Errorf("after %s the tabs are %v, want %v", tc.item, open, tc.left)
			}
		})
	}
}

// tabNamed is the tab whose name holds name.
func tabNamed(s *apptest.Scene, name string) *render.AccessNode {
	s.Frame()
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == ui.RoleTab && strings.Contains(n.Name, name) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.UI().AccessTree())
}
