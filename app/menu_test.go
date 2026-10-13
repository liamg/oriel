package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// rightClick right-clicks at p and draws the menu.
func (s *errScene) rightClick(p render.Point) string {
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonSecondary})
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonSecondary})
	s.frame()
	s.frame()
	return s.frameText()
}

// menuScene has a Go file and a Markdown file open side by side, Problems docked, and a Lua plugin's entry for Go
// files.
func menuScene(t *testing.T) (*errScene, map[string]render.Rect) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme\n"), 0o644)
	s := newScene(t, "", dir, mockupSize)
	s.writeConfig(t, map[string]string{"init.lua": `oriel.menu.add{ label = "Run go vet", group = "go", when = { file = "*.go" },
  run = function(at) oriel.notify("vet " .. at.file) end }
`})
	s.lua().reload(false)
	s.run("editor.open", registry.Args{"path": "main.go"})
	s.run("editor.open", registry.Args{"path": "README.md", "place": "vsplit"})
	s.run("Problems", nil)
	s.frame()
	s.frame()
	rects := map[string]render.Rect{}
	for _, v := range s.sh.layout.root.Visible() {
		if r, ok := s.host.PaneRect(v); ok {
			name := v.ID
			if f, _ := s.editorFile(v.Instance); f != "" {
				name = filepath.Base(f)
			}
			rects[name] = r
		}
	}
	return s, rects
}

func (s *errScene) editorFile(instance string) (string, bool) {
	v, ok := s.reg.ViewByID(EditorView)
	if !ok {
		return "", false
	}
	l, ok := registry.As[registry.Located](v)
	if !ok {
		return "", false
	}
	return l.Location(instance)
}

// PL-13: "A Lua plugin adds an entry to the editor's right-click menu for `*.go` files only; it shows there, not in
// a `.md` file or the Problems view, and runs its function."
func TestPluginMenuEntry(t *testing.T) {
	s, rects := menuScene(t)
	for _, want := range []string{"main.go", "README.md", "problems"} {
		if _, ok := rects[want]; !ok {
			t.Fatalf("no %s pane: %v", want, rects)
		}
	}
	if text := s.rightClick(centre(rects["README.md"])); strings.Contains(text, "Run go vet") || !strings.Contains(text, "? Help") {
		t.Errorf("the Markdown file's menu:\n%s", text)
	}
	s.typeKeys("Esc")
	if text := s.rightClick(centre(rects["problems"])); strings.Contains(text, "Run go vet") {
		t.Errorf("Problems' menu has the Go entry:\n%s", text)
	}
	s.typeKeys("Esc")
	text := s.rightClick(centre(rects["main.go"]))
	if !strings.Contains(text, "Run go vet") {
		t.Fatalf("the Go file's menu lacks the entry:\n%s", text)
	}
	if strings.Index(text, "Run go vet") > strings.Index(text, "? Help") {
		t.Errorf("Help isn't last:\n%s", text)
	}
	b := s.access("Run go vet")
	if b == nil {
		t.Fatal("no menu item to click")
	}
	s.click(centre(b.Bounds))
	if n, _ := lastNotice(s.store.State(), "lua."); !strings.HasSuffix(n.Text, "main.go") || !strings.HasPrefix(n.Text, "vet ") {
		t.Errorf("the entry didn't run: %+v", n)
	}
}

// PL-13: "The built-in entries come from the same contribution point" and "Entries keep their group order, with
// Help last, and a disposed plugin's entries go."
func TestBuiltinMenuEntries(t *testing.T) {
	s, rects := menuScene(t)
	var names []string
	for _, c := range s.reg.Contributions("menu.item") {
		names = append(names, c.ContributionName())
	}
	for _, want := range []string{"editor.copy-path", "pane.move", "pane.close"} {
		if !slices.Contains(names, want) {
			t.Errorf("no built-in %s in %v", want, names)
		}
	}
	text := s.rightClick(centre(rects["main.go"]))
	order := []string{"Copy path", "Run go vet", "Move pane", "Close", "? Help"}
	last := -1
	for _, w := range order {
		i := strings.Index(text, w)
		if i < 0 || i < last {
			t.Fatalf("menu order wants %v:\n%s", order, text)
		}
		last = i
	}
	s.typeKeys("Esc")
	s.writeConfig(t, map[string]string{"init.lua": ""})
	s.lua().reload(false)
	if text := s.rightClick(centre(rects["main.go"])); strings.Contains(text, "Run go vet") {
		t.Errorf("the reloaded-away entry is still there:\n%s", text)
	}
}
