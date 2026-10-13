package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
)

const dndPlugin = `local s = oriel.ui.state("dnd", { got = "nothing" })
oriel.view.override("tickets", function()
  return oriel.ui.column{ pad = 14, gap = 8,
    oriel.ui.text("TICKETS", "accent"),
    oriel.ui.row{ id = "t1", drag = { kind = "demo.ticket", data = "INF-1", label = "INF-1" }, oriel.ui.text("INF-1 fix rounding") },
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

func dndScene(t *testing.T) *errScene {
	s := luaScene(t, dndPlugin)
	s.run("view.open", registry.Args{"id": "tickets", "place": "dock", "edge": "left", "title": "Tickets", "size": 400})
	s.run("view.open", registry.Args{"id": "board", "place": "dock", "edge": "right", "title": "Board", "size": 400})
	s.frame()
	s.frame()
	return s
}

// textAt is the middle of the first run of text reading text.
func (s *errScene) textAt(t *testing.T, text string) render.Point {
	t.Helper()
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no %q in the frame:\n%s", text, s.frameText())
	return render.Point{}
}

// drag presses at from, moves to to in steps (a frame each), and lets go.
func (s *errScene) drag(from, to render.Point) {
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.frame()
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: from.X + (to.X-from.X)*f, Y: from.Y + (to.Y-from.Y)*f}})
		s.frame()
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: to, Buttons: render.ButtonPrimary})
	s.frame()
	s.frame()
}

// PL-15: "A Lua panel row dragged onto another Lua panel's target calls its `on_drop` with the payload; a target
// that doesn't take the kind doesn't glow or receive it."
func TestPluginDragAndDrop(t *testing.T) {
	s := dndScene(t)
	target := s.textAt(t, "Doing ← drop here")
	s.drag(s.textAt(t, "a note"), target)
	if !strings.Contains(s.frameText(), "got nothing") {
		t.Fatalf("a kind the target doesn't take was dropped:\n%s", s.frameText())
	}
	// Half way through a drag of a ticket, the target glows and a ghost follows the pointer.
	from := s.textAt(t, "INF-1 fix rounding")
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: target})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "INF-1\n") && !strings.HasSuffix(s.frameText(), "INF-1") {
		t.Errorf("no ghost under the pointer:\n%s", s.frameText())
	}
	s.golden("dnd")
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: target, Buttons: render.ButtonPrimary})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "got demo.ticket:INF-1") {
		t.Errorf("the drop didn't reach on_drop:\n%s", s.frameText())
	}
}

// PL-15: "Drags work by keyboard too (pick up, move focus, drop), for accessibility."
func TestKeyboardDragAndDrop(t *testing.T) {
	s := dndScene(t)
	src := s.access("INF-1")
	if src == nil {
		t.Fatalf("the draggable row isn't reachable:\n%s", s.frameText())
	}
	s.click(centre(src.Bounds))
	s.keyCommand(t, "<Space>") // pick up
	if s.access("doing") == nil {
		t.Fatal("no drop target to focus")
	}
	for i := 0; i < 10 && !s.access("doing").Focused; i++ {
		s.keyCommand(t, "<Tab>")
	}
	if !s.access("doing").Focused {
		t.Fatal("Tab never reached the drop target")
	}
	s.keyCommand(t, "<Space>") // drop
	s.frame()
	if !strings.Contains(s.frameText(), "got demo.ticket:INF-1") {
		t.Errorf("the keyboard drop didn't land:\n%s", s.frameText())
	}
}

// keyCommand types a key as the window does: through the focused node's key mode, else to the node.
func (s *errScene) keyCommand(t *testing.T, key string) {
	t.Helper()
	if k, _ := s.reg.Lookup(s.sh.keyModesFor(key), keymap.Parse(key)); k != nil {
		s.run(k.Command, k.Args)
	} else {
		s.ui.Key(fromNotation(key))
	}
	s.frame()
}

// PL-15: "Dragging a file from the explorer (or the OS) onto the editor opens it; onto a plugin target that takes
// `file`, the plugin gets the path."
func TestFilesDroppedOnTheWindow(t *testing.T) {
	s := dndScene(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	os.WriteFile(a, []byte("package a\n"), 0o644)
	os.WriteFile(b, []byte("package b\n"), 0o644)
	s.dropFiles(s.textAt(t, "Doing ← drop here"), a, b)
	if !strings.Contains(s.frameText(), "got file:"+a+","+b) {
		t.Errorf("the plugin didn't get the files:\n%s", s.frameText())
	}
	s.dropFiles(render.Point{X: 700, Y: 400}, a) // the middle of the window, where nothing takes files
	s.frame()
	if p, ok := s.editor().e.Pane(s.editor().e.WindowID()); !ok || p.Name() != a {
		t.Errorf("a file dropped on the window didn't open: %+v", p)
	}
}

func (s *errScene) dropFiles(at render.Point, paths ...string) {
	s.sh.dropped(render.DropEvent{Pos: at, Paths: paths})
	s.frame()
	s.frame()
}
