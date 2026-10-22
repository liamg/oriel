package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const dndPlugin = `local s = oriel.ui.state("dnd", { got = "nothing" })
oriel.view.override("tickets", function()
  return oriel.ui.column{ pad = 14, gap = 8,
    oriel.ui.text("TICKETS", "accent"),
    oriel.ui.row{ id = "t1", drag = { kind = "demo.ticket", data = "ENG-1", label = "ENG-1" }, oriel.ui.text("ENG-1 fix rounding") },
    oriel.ui.row{ id = "n1", drag = { kind = "demo.note", data = "a note" }, oriel.ui.text("a note") },
  }
end)
oriel.view.override("board", function()
  return oriel.ui.column{ pad = 14, gap = 8,
    oriel.ui.text("BOARD", "accent"),
    oriel.ui.row{ id = "doing", accepts = { "demo.ticket", "file" },
      on_drop = function(kind, data)
        if type(data) == "table" then data = table.concat(data, ",") end
        s.got = kind .. ":" .. data
      end,
      oriel.ui.text("Doing ← drop here") },
    oriel.ui.text("got " .. s.got),
  }
end)
`

func dndScene(t *testing.T) *apptest.Scene {
	s := apptest.LuaScene(t, dndPlugin)
	s.Run("view.open", registry.Args{"id": "tickets", "place": "dock", "edge": "left", "title": "Tickets", "size": 400})
	s.Run("view.open", registry.Args{"id": "board", "place": "dock", "edge": "right", "title": "Board", "size": 400})
	s.Frame()
	s.Frame()
	return s
}

// A Lua panel row dragged onto another Lua panel's target calls its `on_drop` with the payload; a target
// that doesn't take the kind doesn't glow or receive it.
func TestPluginDragAndDrop(t *testing.T) {
	s := dndScene(t)
	target := s.TextPoint("Doing ← drop here")
	s.Drag(s.TextPoint("a note"), target)
	if !strings.Contains(s.Text(), "got nothing") {
		t.Fatalf("a kind the target doesn't take was dropped:\n%s", s.Text())
	}
	// Half way through a drag of a ticket, the target glows and a ghost follows the pointer.
	from := s.TextPoint("ENG-1 fix rounding")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: target})
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "ENG-1\n") && !strings.HasSuffix(s.Text(), "ENG-1") {
		t.Errorf("no ghost under the pointer:\n%s", s.Text())
	}
	s.Golden("dnd")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: target, Buttons: render.ButtonPrimary})
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "got demo.ticket:ENG-1") {
		t.Errorf("the drop didn't reach on_drop:\n%s", s.Text())
	}
}

// Drags work by keyboard too (pick up, move focus, drop), for accessibility.
func TestKeyboardDragAndDrop(t *testing.T) {
	s := dndScene(t)
	src := s.Node("ENG-1")
	if src == nil {
		t.Fatalf("the draggable row isn't reachable:\n%s", s.Text())
	}
	s.Click(apptest.Centre(src.Bounds), 0)
	s.Press("<Space>") // pick up
	if s.Node("doing") == nil {
		t.Fatal("no drop target to focus")
	}
	for i := 0; i < 10 && !s.Node("doing").Focused; i++ {
		s.Press("<Tab>")
	}
	if !s.Node("doing").Focused {
		t.Fatal("Tab never reached the drop target")
	}
	s.Press("<Space>") // drop
	s.Frame()
	if !strings.Contains(s.Text(), "got demo.ticket:ENG-1") {
		t.Errorf("the keyboard drop didn't land:\n%s", s.Text())
	}
}

// Dragging a file from the explorer (or the OS) onto the editor opens it; onto a plugin target that takes
// `file`, the plugin gets the path.
func TestFilesDroppedOnTheWindow(t *testing.T) {
	s := dndScene(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	os.WriteFile(a, []byte("package a\n"), 0o644)
	os.WriteFile(b, []byte("package b\n"), 0o644)
	dropFiles(s, s.TextPoint("Doing ← drop here"), a, b)
	if !strings.Contains(s.FullText(), "got file:"+a+","+b) {
		t.Errorf("the plugin didn't get the files:\n%s", s.FullText())
	}
	dropFiles(s, render.Point{X: 700, Y: 400}, a) // the middle of the window, where nothing takes files
	s.Frame()
	if w, ok := s.Editor().Window(""); !ok || w.Path != a {
		t.Errorf("a file dropped on the window didn't open: %+v", w)
	}
}

func dropFiles(s *apptest.Scene, at render.Point, paths ...string) {
	s.Drop(render.DropEvent{Pos: at, Paths: paths})
	s.Frame()
	s.Frame()
}
