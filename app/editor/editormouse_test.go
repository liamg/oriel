package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// charAt is the middle of column col of the editor line that reads line, in the frame drawn.
func charAt(s *apptest.Scene, t *testing.T, line string, col int) render.Point {
	t.Helper()
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == line {
			w := op.Run.Advance / float32(len(line))
			return render.Point{X: op.At.X + (float32(col)+.5)*w, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no line %q in the frame:\n%s", line, s.Text())
	return render.Point{}
}

func editorMouseScene(t *testing.T) *apptest.Scene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("round half even\nsecond line\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	open(s, t, "a.txt")
	s.Frame()
	s.Frame()
	return s
}

// term.txt *mouse-overview* ('mousemodel' "popup"): a drag selects from the press to the pointer, *<2-LeftMouse>*
// selects the word and *<3-LeftMouse>* the line; S-click extends. ⌘C, ⌘X and ⌘V copy, cut and paste with the system
// clipboard in any mode, as everywhere else on the platform.
func TestEditorMouseSelectsAndCopies(t *testing.T) {
	s := editorMouseScene(t)
	e := engine(s)

	s.Drag(charAt(s, t, "round half even", 6), charAt(s, t, "round half even", 9))
	if e.Mode() != "visual" {
		t.Fatalf("a drag left the editor in %s", e.Mode())
	}
	s.Press("<D-c>")
	if got, _ := s.Desktop.ReadClipboard(); got != "half" {
		t.Errorf("⌘C copied %q, want %q", got, "half")
	}
	if e.Mode() != "normal" {
		t.Errorf("⌘C left the editor in %s", e.Mode())
	}

	second := charAt(s, t, "second line", 2)
	s.Click(second, 0)
	s.Click(second, 0)
	s.Press("<D-x>")
	if got, _ := s.Desktop.ReadClipboard(); got != "second" {
		t.Errorf("a double click and ⌘X cut %q, want %q", got, "second")
	}
	s.Frame()
	s.Press("<D-v>")
	s.Frame()
	if line := string(engine(s).Buf().Line(1)); line != "second line" {
		t.Errorf("⌘V put back %q, want %q", line, "second line")
	}

	s.Press("<D-a>")
	s.Press("<D-c>")
	if got, _ := s.Desktop.ReadClipboard(); got != "round half even\nsecond line" {
		t.Errorf("⌘A and ⌘C copied %q", got)
	}

	at := charAt(s, t, "round half even", 3)
	s.Click(at, 0)
	s.Click(at, 0)
	s.Click(at, 0)
	if e.Mode() != "visual-line" {
		t.Errorf("a triple click left the editor in %s", e.Mode())
	}
	s.Click(at, 0)
	s.Frame()
	s.Click(at, 0) // a fresh click: clicks a while apart aren't counted together
	s.Click(charAt(s, t, "round half even", 9), render.ModShift)
	if start, ok := e.Visual(); !ok || start.Col != 3 || e.Cursor().Col != 9 {
		t.Errorf("S-click selected %v→%v (%v)", start, e.Cursor(), ok)
	}
}

// Read-only text selects too: a drag across a help page's title and first paragraph copies them with ⌘C.
func TestHelpTextSelects(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Size: render.Point{X: 1400, Y: 900}})
	s.Run("help.open", registry.Args{"id": "shell/quit"})
	s.Frame()
	s.Frame()
	title := charAt(s, t, "Quitting", 0)
	end := charAt(s, t, "Quitting", 7)
	end.X += 30
	s.Drag(title, end)
	s.Press("<D-c>")
	if got, _ := s.Desktop.ReadClipboard(); got != "Quitting" {
		t.Errorf("a drag over the help title copied %q", got)
	}
}

// With wrap off (scroll.txt *scroll-horizontal*) a line longer than the pane scrolls sideways: the cursor taken past
// the right edge brings its end into view, 0 brings the start back, and a sideways wheel scrolls it.
func TestEditorScrollsSideways(t *testing.T) {
	dir := t.TempDir()
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(long+"\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 900, Y: 600}})
	open(s, t, "a.txt")
	s.Frame()
	s.Frame()
	shown := func() string {
		for _, op := range s.List().Ops {
			if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "middle") {
				return op.Run.Text
			}
		}
		return ""
	}
	if !strings.HasPrefix(shown(), "start") {
		t.Fatalf("the line doesn't start at its start: %q", shown())
	}
	s.Press("$")
	s.Frame()
	if got := shown(); !strings.HasSuffix(got, "finish") || strings.HasPrefix(got, "start") {
		t.Errorf("$ didn't scroll the end into view: %q", got)
	}
	s.Press("0")
	s.Frame()
	if !strings.HasPrefix(shown(), "start") {
		t.Errorf("0 didn't scroll back: %q", shown())
	}
	at := s.TextPoint(shown())
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{X: 200}})
	s.Frame()
	s.Frame()
	if strings.HasPrefix(shown(), "start") || engine(s).Left() == 0 {
		t.Errorf("a sideways wheel didn't scroll: left %d, %q", engine(s).Left(), shown())
	}
}

// options.txt *'wrap'*: "When on, lines longer than the width of the window will wrap and displaying continues on the
// next line." The status line's wrap switches it; a click on a later row goes to that row's text, and gj
// (*gj*: "Down [count] display lines") stays in the line.
func TestEditorWrapsLongLines(t *testing.T) {
	dir := t.TempDir()
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(long+"\nnext\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 900, Y: 600}})
	open(s, t, "a.txt")
	s.Frame()
	s.Frame()
	rows := func() []render.Op {
		var out []render.Op
		for _, op := range s.List().Ops {
			if op.Kind == render.OpGlyphs && strings.Contains(long, strings.TrimSuffix(op.Run.Text, "…")) && strings.Contains(op.Run.Text, "middle") {
				out = append(out, op)
			}
		}
		return out
	}
	if n := len(rows()); n != 1 {
		t.Fatalf("wrap is off by default, yet the line takes: %d rows", n)
	}
	s.ClickNode("Wrap")
	e := engine(s)
	if !editorOf(s).Current().Wrap() {
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
	s.Click(charAt(s, t, last, len(last)-1), 0)
	if cur := e.Cursor(); cur.Line != 0 || cur.Col != len(long)-1 {
		t.Errorf("a click on the last row's last letter went to %v", cur)
	}
	s.Press("0")
	s.Press("g")
	s.Press("j")
	if cur := e.Cursor(); cur.Line != 0 || cur.Col != len(rs[0].Run.Text) {
		t.Errorf("gj went to %v, want the second row's start", cur)
	}
	s.Run("editor.wrap.toggle", nil)
	s.Frame()
	if editorOf(s).Current().Wrap() || len(rows()) != 1 {
		t.Errorf("editor.wrap.toggle didn't turn wrap off: %d rows", len(rows()))
	}
}

// A click lands on the character under the pointer with a block between the lines above it.
func TestEditorClickBelowABlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first\nsecond line\nthird\nfourth\nfifth\nsixth\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	open(s, t, "a.txt")
	s.Registry().Decorations("test.block", uireg.DecorationFunc(func(uireg.DecorationCtx) []uireg.Decoration {
		return []uireg.Decoration{{Kind: uireg.Block, Line: 0, Node: ui.ColumnNode(ui.TextNode("a card"), ui.TextNode("of three"), ui.TextNode("lines"))}}
	}))
	s.Frame()
	s.Frame()
	s.Click(charAt(s, t, "second line", 7), 0)
	if got := engine(s).Cursor(); got.Line != 1 || got.Col != 7 {
		t.Errorf("a click on %q put the cursor at %+v, want line 1 col 7", "second line", got)
	}
}

// options.txt 'mousemodel' popup_setpos: "the cursor will be moved to the position where the mouse was clicked, and
// thus the selected operation will act upon the clicked object. If clicking inside a selection, that selection will
// be acted upon, i.e. no cursor move."
func TestEditorRightClickPlacesTheCursor(t *testing.T) {
	s := editorMouseScene(t)
	e := engine(s)
	s.RightClick(charAt(s, t, "second line", 7))
	s.Press("<Esc>")
	if got := e.Cursor(); got.Line != 1 || got.Col != 7 {
		t.Errorf("a right-click put the cursor at %+v, want line 1 col 7", got)
	}
	s.Drag(charAt(s, t, "round half even", 6), charAt(s, t, "round half even", 9))
	s.RightClick(charAt(s, t, "round half even", 7))
	s.Press("<Esc>")
	if got := e.Cursor(); got.Line != 0 || got.Col != 9 {
		t.Errorf("a right-click inside the selection moved the cursor to %+v", got)
	}
	s.Drag(charAt(s, t, "round half even", 6), charAt(s, t, "round half even", 9))
	s.RightClick(charAt(s, t, "second line", 2))
	if e.Mode() != "normal" {
		t.Errorf("a right-click outside the selection left the editor in %s", e.Mode())
	}
}
