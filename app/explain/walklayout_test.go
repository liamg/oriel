package explain_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/render"
)

// codeEnd is the right end of the furthest code drawn left of x, between the tops of the rows at top and bottom.
func codeEnd(s *apptest.Scene, x, top, bottom float32) float32 {
	end := float32(0)
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.At.X < x && op.At.Y > top && op.At.Y < bottom {
			end = max(end, op.At.X+op.Run.Advance)
		}
	}
	return end
}

// On a wide window the notes sit beside the code they're about, not at the far side of the window.
func TestExplainNotesSitBesideTheCode(t *testing.T) {
	s, _, _, _ := apptest.WalkSceneAt(t, render.Point{X: 2560, Y: 1000})
	s.Run("explain.change", nil)
	walkSettle(s)
	walkKeys(s, "<Right>")
	walkSettle(s)
	s.SettleGit()
	note, ok := s.TextStart("ROUNDED ONCE")
	if !ok {
		t.Fatalf("no note:\n%s", s.Text())
	}
	first, _ := s.TextStart("package")
	last := s.TextPoint("10")
	end := codeEnd(s, note.X-50, first.Y-5, last.Y+15) // left of the card's lettered badge
	if gap := note.X - end; gap > 160 || gap < 20 {
		t.Errorf("the note's card starts %vpx past the end of the code (at %v, the code ends at %v): want it close beside it", gap, note.X, end)
	}
	s.Golden("explain-change-wide")
}

// A step whose own lines are well above its note opens with the note in view, with a few lines above it, rather
// than at the step's first line with the note out of sight below.
func TestExplainStepOpensOnItsNote(t *testing.T) {
	s, f, _, _ := apptest.WalkSceneAt(t, render.Point{X: 1600, Y: 600})
	f.SetAnswer("```json\n" + `{"steps": [
 {"title": "Rounding is one function", "project": "pricing", "file": "round.go", "lines": [3, 4], "prose": "Everything goes through it.",
  "notes": [{"title": "Currencies it can't round", "lines": [22, 24], "text": "An unknown currency fails."}]},
 {"title": "Summary", "prose": "One place rounds."}]}` + "\n```\n")
	s.Run("explain.change", nil)
	walkSettle(s)
	if !strings.Contains(s.Text(), "CURRENCIES IT CAN'T ROUND") {
		t.Errorf("the step opened without its note in view:\n%s", s.Text())
	}
	if !strings.Contains(s.Text(), `"unknown currency %q"`) {
		t.Errorf("the note's lines aren't in view:\n%s", s.Text())
	}
}
