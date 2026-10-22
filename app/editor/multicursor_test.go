package editor_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// ⌃n in the editor adds cursors at each next match, every one is drawn, the mode says how many, and typing
// goes to all of them.
func TestMultiCursorInTheEditor(t *testing.T) {
	s, root := searchScene(t)
	s.Run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	waitHighlights(s)
	keys(s, "3G6|")
	for range 2 {
		s.Press("<C-n>")
	}
	s.Frame()
	m, ok := engine(s).Multi()
	if !ok || len(m.Regions) != 2 {
		t.Fatalf("after ⌃n ⌃n: %+v", m)
	}
	if n := strings.Count(s.Text(), "\n"); n == 0 {
		t.Fatal("no frame")
	}
	if got := s.Count("cursor"); got != 2 {
		t.Errorf("%d cursors drawn, want 2", got)
	}
	s.WantFrame("2 CURSORS")
	wantTextOverSelection(s, t, "Round")
	if mode, n := s.Node("Mode"), s.Node("Cursors"); mode.Bounds.Min.Y != n.Bounds.Min.Y || n.Bounds.Min.X < mode.Bounds.Max.X {
		t.Errorf("the cursors chip at %v isn't beside the mode at %v", n.Bounds, mode.Bounds)
	}
	keys(s, "cR<Esc><Esc>")
	if got := s.Line(2); got != "func R(v int) int { return v * 100 }" {
		t.Errorf("line 3: %q", got)
	}
	if got := s.Line(4); got != "func RoundMinor(v int) int { return R(v) / 100 }" { // RoundMinor isn't a whole-word match
		t.Errorf("line 5: %q", got)
	}
	keys(s, "1G")
	s.Press("<C-Down>")
	if m, ok := engine(s).Multi(); !ok || len(m.Regions) != 2 || m.Extend {
		t.Errorf("⌃↓ didn't add a cursor below: %+v", m)
	}
}

// wantTextOverSelection checks the selection boxes are painted before (under) the text run reading text.
func wantTextOverSelection(s *apptest.Scene, t *testing.T, text string) {
	t.Helper()
	sel := s.UI().Theme().C(theme.Selection)
	glyphs, under := -1, -1
	for i, op := range s.List().Ops {
		switch {
		case op.Kind == render.OpGlyphs && op.Run.Text == text && glyphs < 0:
			glyphs = i
		case op.Kind == render.OpRect && op.Style.Fill == sel && under < 0:
			under = i
		}
	}
	if glyphs < 0 || under < 0 || under > glyphs {
		t.Errorf("the selection (op %d) isn't under the text %q (op %d)", under, text, glyphs)
	}
}
