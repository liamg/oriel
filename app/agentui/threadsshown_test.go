package agentui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// threadsFile is a Go file with threads on lines 4 (two) and 7.
func threadsFile(t *testing.T) *apptest.Scene {
	dir := t.TempDir()
	src := "package pricing\n\nfunc Round() {\n\tfloor := scaled.Floor()\n\t_ = floor\n\n\treturn\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "round.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
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

// A long thread shows its first comment and its latest few; the ones between expand and collapse again.
func TestLongThreadCollapses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "round.go"), []byte("package pricing\n\nfunc Round() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.SetNow(apptest.At(14, 30, 0))
	th := core.Thread{ID: "t1000000", Status: core.ThreadOpen, Created: s.Now(), Updated: s.Now(),
		Anchor: &core.Anchor{Project: "notes", File: "round.go", Line: 3, Text: "func Round() {}"}}
	for i := range 6 {
		th.Comments = append(th.Comments, core.Comment{ID: fmt.Sprint("c", i), Author: core.AuthorYou, Name: "Liam",
			Body: fmt.Sprintf("Comment number %d", i), Time: s.Now(), Delivered: true})
	}
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects: []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}}, Threads: []core.Thread{th}}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	s.Run("editor.open", registry.Args{"path": "round.go"})
	s.Frames(3)

	collapsed := func(want bool) {
		t.Helper()
		text := s.Text()
		if !apptest.HasAll(text, "Comment number 0", "Comment number 4", "Comment number 5") {
			t.Fatalf("the first and latest comments don't show:\n%s", text)
		}
		for _, mid := range []string{"Comment number 1", "Comment number 2", "Comment number 3"} {
			if strings.Contains(text, mid) == want {
				t.Fatalf("collapsed is %v, yet %q showing is %v:\n%s", want, mid, !want, text)
			}
		}
	}
	collapsed(true)
	s.ClickNode("Show 3 earlier comments")
	collapsed(false)
	s.ClickNode("Show fewer")
	collapsed(true)

	if err := s.Registry().Options().Set("editor.recent_comments", registry.LayerUser, registry.At{}, 4); err != nil {
		t.Fatal(err)
	}
	s.Frames(2)
	s.Node("Show 1 earlier comment")
}

// ␣H flips editor.resolved_threads, kept in the user's settings.
func TestResolvedThreadsToggle(t *testing.T) {
	s := threadsFile(t)
	s.Run("editor.open", registry.Args{"path": "round.go"})
	s.Frames(2)
	s.Click(s.TextPoint("round.go"), 0)
	resolved := func() string {
		v, _ := registry.Option[string](s.Registry().Options(), "editor.resolved_threads", registry.At{})
		return v
	}
	for _, want := range []string{"hidden", "shown"} {
		s.Press("<Space>H")
		s.Frames(2)
		if got := resolved(); got != want {
			t.Fatalf("␣H left editor.resolved_threads %q, not %q", got, want)
		}
	}
}

// A presentation has a resolved button, with the count of the file's resolved threads, that hides and shows them; the
// text, which never shows resolved threads, has none.
func TestResolvedThreadsButton(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n\nFirst point.\n\nSecond point.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.SetNow(apptest.At(14, 30, 0))
	th := core.Thread{ID: "t1000000", Status: core.ThreadResolved, Created: s.Now(), Updated: s.Now(),
		Anchor:   &core.Anchor{Project: "notes", File: "notes.md", Line: 3, Text: "First point."},
		Comments: []core.Comment{{ID: "c0", Author: core.AuthorYou, Name: "Liam", Body: "Settled", Time: s.Now(), Delivered: true}}}
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects: []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}}, Threads: []core.Thread{th}}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	s.Run("editor.open", registry.Args{"path": "notes.md"})
	s.Frames(3)
	if s.Find("resolved 1") != nil {
		t.Fatal("the text shows a resolved button, though it never shows resolved threads")
	}
	s.Run("editor.presenter.open", registry.Args{"name": "markdown"})
	s.Frames(3)
	resolved := func() string {
		v, _ := registry.Option[string](s.Registry().Options(), "editor.resolved_threads", registry.At{})
		return v
	}
	for _, want := range []string{"hidden", "shown"} {
		s.ClickNode("resolved 1")
		s.Frames(2)
		if got := resolved(); got != want {
			t.Fatalf("the resolved button left editor.resolved_threads %q, not %q", got, want)
		}
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
