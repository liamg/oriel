package gitui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// longDiff is a scene with notes.txt's diff open, its second line long, in layout; it waits for the line's start.
func longDiff(t *testing.T, layout, long string) *apptest.Scene {
	t.Helper()
	r := gittest.New(t)
	r.Write(map[string]string{"notes.txt": "first\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"notes.txt": "first\n" + long + "\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	if err := s.Registry().Options().Set("diff.layout", registry.LayerUser, registry.At{}, layout); err != nil {
		t.Fatal(err)
	}
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "start") })
	s.Quiet()
	return s
}

// inView says text is drawn whole, not scrolled past or cut at its box's edge.
func inView(s *apptest.Scene, text string) bool {
	_, ok := s.Shows(text)
	return ok
}

// The diff's own wrap control switches 'wrap', as the editor's status line does: the long line takes many rows, then
// one again.
//
// ref: options.txt *'wrap'*
// quote: "When on, lines longer than the width of the window will wrap and displaying continues on the next line."
func TestDiffWrapControl(t *testing.T) {
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	for _, layout := range []string{"split", "unified"} {
		t.Run(layout, func(t *testing.T) {
			s := longDiff(t, layout, long)
			if n := len(rowsOf(s, long)); n != 1 {
				t.Fatalf("with nowrap the line takes %d rows", n)
			}
			s.ClickOn("Wrap long lines")
			s.Quiet()
			if n := len(rowsOf(s, long)); n < 2 || !inView(s, "finish") {
				t.Fatalf("the wrap control left the line on %d rows, its end shown %v", n, inView(s, "finish"))
			}
			if n := s.FindRole(render.RoleButton, "Wrap long lines"); n == nil || !n.Selected {
				t.Error("the wrap control isn't lit while lines wrap")
			}
			s.ClickOn("Wrap long lines")
			s.Quiet()
			if n := len(rowsOf(s, long)); n != 1 {
				t.Fatalf("with wrap switched off again the line takes %d rows", n)
			}
		})
	}
}

// With wrap off the diff scrolls across, a file's lines together and its line numbers staying put: by the sideways
// wheel (or shift and the wheel), and by zL and zH.
//
// ref: options.txt *'wrap'*
// quote: "When off lines will not wrap and only part of long lines will be displayed."
// ref: scroll.txt *zL*
// quote: "Move the view on the text half a screenwidth to the right"
// ref: scroll.txt *zH*
// quote: "Move the view on the text half a screenwidth to the left"
func TestDiffScrollsSideways(t *testing.T) {
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	for _, layout := range []string{"split", "unified"} {
		t.Run(layout, func(t *testing.T) {
			s := longDiff(t, layout, long)
			if inView(s, "finish") {
				t.Fatal("the long line's end shows before scrolling")
			}
			if !inView(s, "start") {
				t.Fatal("the long line's start doesn't show")
			}
			at, _ := s.Shows("start")
			s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{X: 100000}})
			s.Quiet()
			if !inView(s, "finish") {
				t.Fatalf("the sideways wheel didn't bring the line's end into view:\n%s", s.Text())
			}
			if inView(s, "start") {
				t.Error("the line's start still shows, scrolled to its end")
			}
			if !inView(s, "2") {
				t.Error("the gutter's line numbers scrolled away with the code")
			}

			// zH comes back by halves, zL goes again.
			for range 40 {
				if inView(s, "start") {
					break
				}
				s.Press("zH")
				s.Quiet()
			}
			if !inView(s, "start") || inView(s, "finish") {
				t.Fatal("zH didn't bring the line's start back")
			}
			for range 40 {
				if inView(s, "finish") {
					break
				}
				s.Press("zL")
				s.Quiet()
			}
			if !inView(s, "finish") {
				t.Fatal("zL didn't bring the line's end into view")
			}
		})
	}
}
