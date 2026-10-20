package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// charAt is the middle of column col of the editor line that reads line, in the frame drawn.
func (s *errScene) charAt(t *testing.T, line string, col int) render.Point {
	t.Helper()
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == line {
			w := op.Run.Advance / float32(len(line))
			return render.Point{X: op.At.X + (float32(col)+.5)*w, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no line %q in the frame:\n%s", line, s.frameText())
	return render.Point{}
}

func editorMouseScene(t *testing.T) *errScene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("round half even\nsecond line\n"), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1200, Y: 800})
	s.open(t, "a.txt")
	s.frame()
	s.frame()
	return s
}

// term.txt *mouse-overview* ('mousemodel' "popup"): a drag selects from the press to the pointer, *<2-LeftMouse>*
// selects the word and *<3-LeftMouse>* the line; S-click extends. ⌘C, ⌘X and ⌘V copy, cut and paste with the system
// clipboard in any mode, as everywhere else on the platform.
func TestEditorMouseSelectsAndCopies(t *testing.T) {
	s := editorMouseScene(t)
	e := s.editor().e

	s.drag(s.charAt(t, "round half even", 6), s.charAt(t, "round half even", 9))
	if e.Mode() != "visual" {
		t.Fatalf("a drag left the editor in %s", e.Mode())
	}
	s.keyCommand(t, "<D-c>")
	if got, _ := s.clip.ReadClipboard(); got != "half" {
		t.Errorf("⌘C copied %q, want %q", got, "half")
	}
	if e.Mode() != "normal" {
		t.Errorf("⌘C left the editor in %s", e.Mode())
	}

	second := s.charAt(t, "second line", 2)
	s.click(second)
	s.click(second)
	s.keyCommand(t, "<D-x>")
	if got, _ := s.clip.ReadClipboard(); got != "second" {
		t.Errorf("a double click and ⌘X cut %q, want %q", got, "second")
	}
	s.frame()
	s.keyCommand(t, "<D-v>")
	s.frame()
	if line := string(s.editor().e.Buf().Line(1)); line != "second line" {
		t.Errorf("⌘V put back %q, want %q", line, "second line")
	}

	s.keyCommand(t, "<D-a>")
	s.keyCommand(t, "<D-c>")
	if got, _ := s.clip.ReadClipboard(); got != "round half even\nsecond line" {
		t.Errorf("⌘A and ⌘C copied %q", got)
	}

	at := s.charAt(t, "round half even", 3)
	s.click(at)
	s.click(at)
	s.click(at)
	if e.Mode() != "visual-line" {
		t.Errorf("a triple click left the editor in %s", e.Mode())
	}
	s.click(at)
	s.frame()
	s.click(at) // a fresh click: clicks a while apart aren't counted together
	s.clickWith(s.charAt(t, "round half even", 9), render.ModShift)
	if start, ok := e.Visual(); !ok || start.Col != 3 || e.Cursor().Col != 9 {
		t.Errorf("S-click selected %v→%v (%v)", start, e.Cursor(), ok)
	}
}

// Read-only text selects too: a drag across a help page's title and first paragraph copies them with ⌘C.
func TestHelpTextSelects(t *testing.T) {
	s := newScene(t, "", t.TempDir(), render.Point{X: 1400, Y: 900})
	s.run("help.open", registry.Args{"id": "shell/quit"})
	s.frame()
	s.frame()
	title := s.charAt(t, "Quitting", 0)
	end := s.charAt(t, "Quitting", 7)
	end.X += 30
	s.drag(title, end)
	s.keyCommand(t, "<D-c>")
	if got, _ := s.clip.ReadClipboard(); got != "Quitting" {
		t.Errorf("a drag over the help title copied %q", got)
	}
}

// With wrap off (scroll.txt *scroll-horizontal*) a line longer than the pane scrolls sideways: the cursor taken past
// the right edge brings its end into view, 0 brings the start back, and a sideways wheel scrolls it.
func TestEditorScrollsSideways(t *testing.T) {
	dir := t.TempDir()
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(long+"\n"), 0o644)
	s := newScene(t, "", dir, render.Point{X: 900, Y: 600})
	s.open(t, "a.txt")
	s.frame()
	s.frame()
	shown := func() string {
		for _, op := range s.list.Ops {
			if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "middle") {
				return op.Run.Text
			}
		}
		return ""
	}
	if !strings.HasPrefix(shown(), "start") {
		t.Fatalf("the line doesn't start at its start: %q", shown())
	}
	s.keyCommand(t, "$")
	s.frame()
	if got := shown(); !strings.HasSuffix(got, "finish") || strings.HasPrefix(got, "start") {
		t.Errorf("$ didn't scroll the end into view: %q", got)
	}
	s.keyCommand(t, "0")
	s.frame()
	if !strings.HasPrefix(shown(), "start") {
		t.Errorf("0 didn't scroll back: %q", shown())
	}
	at := s.textAt(t, shown())
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{X: 200}})
	s.frame()
	s.frame()
	if strings.HasPrefix(shown(), "start") || s.editor().e.Left() == 0 {
		t.Errorf("a sideways wheel didn't scroll: left %d, %q", s.editor().e.Left(), shown())
	}
}

// options.txt *'wrap'*: "When on, lines longer than the width of the window will wrap and displaying continues on the
// next line." The status line's wrap switches it; a click on a later row goes to that row's text, and gj
// (*gj*: "Down [count] display lines") stays in the line.
func TestEditorWrapsLongLines(t *testing.T) {
	dir := t.TempDir()
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(long+"\nnext\n"), 0o644)
	s := newScene(t, "", dir, render.Point{X: 900, Y: 600})
	s.open(t, "a.txt")
	s.frame()
	s.frame()
	rows := func() []render.Op {
		var out []render.Op
		for _, op := range s.list.Ops {
			if op.Kind == render.OpGlyphs && strings.Contains(long, strings.TrimSuffix(op.Run.Text, "…")) && strings.Contains(op.Run.Text, "middle") {
				out = append(out, op)
			}
		}
		return out
	}
	if n := len(rows()); n != 1 {
		t.Fatalf("wrap is off by default, yet the line takes: %d rows", n)
	}
	s.clickAccess(t, "Wrap")
	e := s.editor().e
	if !s.editor().current().Wrap() {
		t.Fatal("the status line's wrap didn't turn wrap on")
	}
	rs := rows()
	if len(rs) < 3 {
		t.Fatalf("the long line takes %d rows", len(rs))
	}
	var joined string
	for i, r := range rs {
		joined += r.Run.Text
		if i > 0 && r.At.Y <= rs[i-1].At.Y {
			t.Errorf("row %d isn't below row %d", i, i-1)
		}
	}
	if joined != long {
		t.Errorf("the rows don't make up the line:\n%q", joined)
	}
	last := rs[len(rs)-1].Run.Text
	s.click(s.charAt(t, last, len(last)-1))
	if cur := e.Cursor(); cur.Line != 0 || cur.Col != len(long)-1 {
		t.Errorf("a click on the last row's last letter went to %v", cur)
	}
	s.keyCommand(t, "0")
	s.keyCommand(t, "g")
	s.keyCommand(t, "j")
	if cur := e.Cursor(); cur.Line != 0 || cur.Col != len(rs[0].Run.Text) {
		t.Errorf("gj went to %v, want the second row's start", cur)
	}
	s.run("editor.wrap.toggle", nil)
	s.frame()
	if s.editor().current().Wrap() || len(rows()) != 1 {
		t.Errorf("editor.wrap.toggle didn't turn wrap off: %d rows", len(rows()))
	}
}
