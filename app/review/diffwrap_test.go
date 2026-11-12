package review_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
)

// A review's diff has its own wrap control though no editor is open: on, the PR's long line wraps; off, the diff
// scrolls across to its end.
//
// ref: options.txt *'wrap'*
// quote: "When on, lines longer than the width of the window will wrap and displaying continues on the next line."
// ref: options.txt *'wrap'*
// quote: "When off lines will not wrap and only part of long lines will be displayed."
func TestReviewDiffWrapsAndScrollsAcross(t *testing.T) {
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	s, _, _, _, _, _ := prReviewSceneOf(t, false, "package ratelimit\n\nfunc Take() int { return 1 }\n\n// "+long+"\n")
	s.Until("the PR's diff", func() bool { return s.Showing(kit.DiffView) && strings.Contains(s.Text(), "start") })
	s.Quiet()
	rows := func() int {
		ys := map[float32]bool{}
		for _, op := range s.List().Ops {
			if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "middle") {
				ys[op.At.Y] = true
			}
		}
		return len(ys)
	}
	if n := rows(); n != 1 {
		t.Fatalf("with nowrap the line takes %d rows", n)
	}
	if _, ok := s.Shows("finish"); ok {
		t.Fatal("the long line's end shows before scrolling")
	}

	s.ClickOn("Wrap long lines")
	s.Quiet()
	if n := rows(); n < 2 {
		t.Fatalf("wrapped, the line takes %d rows", n)
	}
	s.ClickOn("Wrap long lines")
	s.Quiet()
	if n := rows(); n != 1 {
		t.Fatalf("unwrapped again, the line takes %d rows", n)
	}

	at, _ := s.Shows("start")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{X: 100000}})
	s.Quiet()
	if _, ok := s.Shows("finish"); !ok {
		t.Fatal("the sideways wheel didn't bring the line's end into view")
	}
}
