package agentui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// threadsFile is a Go file with threads on lines 4 (two) and 7.
func threadsFile(t *testing.T) *apptest.Scene {
	dir := t.TempDir()
	src := "package pricing\n\nfunc Round() {\n\tfloor := scaled.Floor()\n\t_ = floor\n\n\treturn\n}\n"
	os.WriteFile(filepath.Join(dir, "round.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte(src), 0o644)
	return apptest.ThreadsScene(t, apptest.Options{WorkDir: dir},
		[4]string{"round.go", "4", "\tfloor := scaled.Floor()", "Floor goes the wrong way for negatives"},
		[4]string{"round.go", "4", "\tfloor := scaled.Floor()", "And for zero?"},
		[4]string{"round.go", "7", "\treturn", "Return the rounded value"},
		[4]string{"other.go", "7", "\treturn", "Other file's thread"})
}

// focusedNamed is the focused accessible node named name, or nil.
func focusedNamed(s *apptest.Scene, name string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Name == name && n.Focused {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// threads.toggle hides a file's threads behind gutter bubbles; hovering one shows its popover, which goes when the
// pointer leaves; clicking shows the threads at that one.
func TestThreadsHideBehindBubbles(t *testing.T) {
	s := threadsFile(t)
	s.Run("editor.open", registry.Args{"path": "round.go"})
	s.Frames(3)
	for _, want := range []string{"Floor goes the wrong way for negatives", "Return the rounded value"} {
		if !strings.Contains(s.Text(), want) {
			t.Fatalf("the threads don't show at first: no %q", want)
		}
	}
	s.Node("threads 3")
	s.Run("threads.toggle", nil)
	s.Frames(3)
	if text := s.Text(); strings.Contains(text, "Floor goes the wrong way") || strings.Contains(text, "Return the rounded value") {
		t.Fatalf("threads.toggle didn't hide the threads:\n%s", text)
	}
	bubble := s.Node("2 threads")
	s.Node("1 thread")
	focus := s.UI().Focused()

	// Hovering the bubble shows the popover; the pointer can move into it; it goes when the pointer leaves.
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(bubble.Bounds)})
	s.Frames(2)
	if text := s.Text(); !apptest.HasAll(text, "Floor goes the wrong way for negatives", "And for zero?", "OPEN") {
		t.Fatalf("no popover over the bubble:\n%s", text)
	}
	pop := s.Node("Threads on line 4")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: pop.Bounds.Max.X - 12, Y: pop.Bounds.Max.Y - 8}})
	s.Frames(2)
	if !strings.Contains(s.Text(), "And for zero?") {
		t.Error("the popover went when the pointer moved into it")
	}
	if s.UI().Focused() != focus {
		t.Error("the popover took focus")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: s.Size().X - 20, Y: s.Size().Y - 60}})
	s.Frames(2)
	if strings.Contains(s.Text(), "And for zero?") {
		t.Error("the popover stays with the pointer gone")
	}

	// A click on the bubble shows the threads again, at that one, its reply field ready.
	s.Click(apptest.Centre(s.Node("2 threads").Bounds), 0)
	s.Frames(3)
	if text := s.Text(); !apptest.HasAll(text, "Floor goes the wrong way for negatives", "Return the rounded value") {
		t.Fatalf("a click on the bubble didn't show the threads:\n%s", text)
	}
	if n := focusedNamed(s, "Reply to the thread"); n == nil {
		t.Error("the thread's reply field isn't ready")
	}

	// Hidden again, ⏎ on a line with threads shows them.
	s.Run("threads.toggle", nil)
	s.Frames(2)
	s.Click(s.TextPoint("round.go"), 0)
	s.EditorKeys("7G")
	s.Frames(2)
	s.Press("<CR>")
	s.Frames(2)
	if !strings.Contains(s.Text(), "Return the rounded value") {
		t.Errorf("⏎ on line 7 didn't show its thread:\n%s", s.Text())
	}
}

// editor.threads = "hidden" opens files with their threads behind bubbles.
func TestThreadsHiddenOption(t *testing.T) {
	s := threadsFile(t)
	if err := s.Registry().Options().Set("editor.threads", registry.LayerUser, registry.At{}, "hidden"); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": "other.go"})
	s.Frames(3)
	if strings.Contains(s.Text(), "Other file's thread") || s.Find("1 thread") == nil {
		t.Errorf("other.go opened with its threads showing:\n%s", s.Text())
	}
}
