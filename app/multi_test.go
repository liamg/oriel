package app

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// ED-8: ⌃n in the editor adds cursors at each next match, every one is drawn, the mode says how many, and typing
// goes to all of them.
func TestMultiCursorInTheEditor(t *testing.T) {
	s, root := searchScene(t)
	s.run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	s.frame()
	s.keys("3G6|")
	for range 2 {
		s.keyCommand(t, "<C-n>")
	}
	s.frame()
	m, ok := s.editor().e.Multi()
	if !ok || len(m.Regions) != 2 {
		t.Fatalf("after ⌃n ⌃n: %+v", m)
	}
	if n := strings.Count(s.frameText(), "\n"); n == 0 {
		t.Fatal("no frame")
	}
	if got := s.countAccess("cursor"); got != 2 {
		t.Errorf("%d cursors drawn, want 2", got)
	}
	s.wantText(t, "the mode", "2 CURSORS")
	s.wantTextOverSelection(t, "Round")
	if mode, n := s.access("Mode"), s.access("Cursors"); mode.Bounds.Min.Y != n.Bounds.Min.Y || n.Bounds.Min.X < mode.Bounds.Max.X {
		t.Errorf("the cursors chip at %v isn't beside the mode at %v", n.Bounds, mode.Bounds)
	}
	s.keys("cR<Esc><Esc>")
	if got := s.line(2); got != "func R(v int) int { return v * 100 }" {
		t.Errorf("line 3: %q", got)
	}
	if got := s.line(4); got != "func RoundMinor(v int) int { return R(v) / 100 }" { // RoundMinor isn't a whole-word match
		t.Errorf("line 5: %q", got)
	}
	s.keys("1G")
	s.keyCommand(t, "<C-Down>")
	if m, ok := s.editor().e.Multi(); !ok || len(m.Regions) != 2 || m.Extend {
		t.Errorf("⌃↓ didn't add a cursor below: %+v", m)
	}
}

func (s *errScene) countAccess(label string) int {
	n := 0
	var walk func(*render.AccessNode)
	walk = func(a *render.AccessNode) {
		if a.Name == label {
			n++
		}
		for _, k := range a.Children {
			walk(k)
		}
	}
	walk(s.ui.AccessTree())
	return n
}

// ED-8: a :s of a whole word offers the language server's rename instead, and ⇥ takes it, showing its diff.
func TestSubstituteOffersLSPRename(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.keys(`:%s/\<greeting\>/salute/g`)
	s.lspSettle()
	s.wantText(t, "the command line", "1 match", "⇥ use LSP rename")
	s.keyCommand(t, "<Tab>")
	s.lspSettle()
	if !s.showing(ReplaceView) {
		t.Fatalf("⇥ didn't show the rename:\n%s", s.frameText())
	}
	s.wantText(t, "the rename", "Rename greeting → salute")
	if _, ok := s.editor().e.Cmdline(); ok {
		t.Error("the command line stayed open")
	}
}

// wantTextOverSelection checks the selection boxes are painted before (under) the text run reading text.
func (s *errScene) wantTextOverSelection(t *testing.T, text string) {
	t.Helper()
	sel := s.ui.Theme().C(theme.Selection)
	glyphs, under := -1, -1
	for i, op := range s.list.Ops {
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
