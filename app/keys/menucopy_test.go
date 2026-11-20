package keys_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// choose clicks the right-click menu's item label, which must be there and enabled.
func choose(t *testing.T, s *apptest.Scene, label string) {
	t.Helper()
	n := s.FindRole(render.RoleMenuItem, label)
	if n == nil {
		t.Fatalf("no %q in the menu:\n%s", label, s.Text())
	}
	if n.Disabled {
		t.Fatalf("%q is disabled:\n%s", label, s.Text())
	}
	s.Click(apptest.Centre(n.Bounds), 0)
	s.Frames(2)
}

// help ui/context-menu: "In a text field: Cut, Copy, Paste and Select all." Each does as its key would, in the field
// right-clicked.
func TestATextFieldsMenuCutsCopiesAndPastes(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Run("settings.open", nil)
	s.Frames(2)
	field := s.FindRole(render.RoleTextBox, "Search settings")
	if field == nil {
		t.Fatalf("no settings search field:\n%s", s.Text())
	}
	s.Click(apptest.Centre(field.Bounds), 0)
	s.TypeText("round half")
	s.Frames(2)

	text := s.RightClick(apptest.Centre(field.Bounds))
	for _, want := range []string{"Cut", "Copy", "Paste", "Select all"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the field's menu lacks %s:\n%s", want, text)
		}
	}
	if n := s.FindRole(render.RoleMenuItem, "Cut"); n == nil || !n.Disabled {
		t.Error("Cut is offered with nothing selected")
	}
	choose(t, s, "Select all")
	s.RightClick(apptest.Centre(field.Bounds))
	choose(t, s, "Cut")
	if s.Desktop.Text != "round half" {
		t.Errorf("cut put %q on the clipboard", s.Desktop.Text)
	}
	if v := s.FindRole(render.RoleTextBox, "Search settings").Value; v != "" {
		t.Errorf("cut left %q in the field", v)
	}
	s.RightClick(apptest.Centre(field.Bounds))
	choose(t, s, "Paste")
	if v := s.FindRole(render.RoleTextBox, "Search settings").Value; v != "round half" {
		t.Errorf("paste gave %q", v)
	}
	s.RightClick(apptest.Centre(field.Bounds))
	choose(t, s, "Select all")
	s.RightClick(apptest.Centre(field.Bounds))
	s.Desktop.Text = ""
	choose(t, s, "Copy")
	if s.Desktop.Text != "round half" {
		t.Errorf("copy put %q on the clipboard", s.Desktop.Text)
	}
}

// help ui/context-menu: "On text you've selected in a page (a comment, a doc, help): Copy."
func TestSelectedPageTextCopies(t *testing.T) {
	s, _ := menuScene(t)
	s.Run("help.open", registry.Args{"id": "ui/context-menu"})
	s.Frames(3)
	line := "Right-click anything for what you can do with it. The last item is always its help."
	r, ok := s.TextRect(line)
	if !ok {
		t.Fatalf("no help text to select:\n%s", s.Text())
	}
	y := apptest.Centre(r).Y
	s.Drag(render.Point{X: r.Min.X + 1, Y: y}, render.Point{X: r.Max.X - 1, Y: y})
	s.Frames(2)
	sel := s.UI().SelectedText()
	if sel == "" {
		t.Fatal("dragging across the help selected nothing")
	}
	text := s.RightClick(apptest.Centre(r))
	if !strings.Contains(text, "Copy") {
		t.Fatalf("the menu over selected text lacks Copy:\n%s", text)
	}
	choose(t, s, "Copy")
	if s.Desktop.Text != sel {
		t.Errorf("copy put %q on the clipboard, want the selection %q", s.Desktop.Text, sel)
	}
}

// help ui/context-menu: "In the editor: Cut, Copy and Paste". Copy takes the selection right-clicked in.
func TestTheEditorsMenuCopiesItsSelection(t *testing.T) {
	s, rects := menuScene(t)
	s.Click(apptest.Centre(rects["main.go"]), 0)
	s.Run("editor.all.select", nil)
	s.Frames(2)
	text := s.RightClickText("package main") // inside the selection, which a right-click there keeps
	for _, want := range []string{"Cut", "Copy", "Paste"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the editor's menu lacks %s:\n%s", want, text)
		}
	}
	choose(t, s, "Copy")
	if !strings.Contains(s.Desktop.Text, "func main() {}") {
		t.Errorf("copy put %q on the clipboard, want the buffer selected", s.Desktop.Text)
	}
}
