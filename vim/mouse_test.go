package vim_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/vim"
)

// term.txt *mouse-overview*, with 'mousemodel' "popup": "<LeftDrag> yes start or extend (1) no" in Normal mode, and in
// Insert mode "<LeftDrag> yes start or extend (1) no like CTRL-O (1)". A drag makes the Visual area run from where the
// press was to where the pointer is; a click after it ("<LeftMouse> yes end yes") ends it.
func TestMouseSelects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "round half even\nsecond line\n")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>")

	s.e.Select(vim.Pos{Line: 0, Col: 6}, vim.Pos{Line: 0, Col: 9}, 'v')
	if s.e.Mode() != "visual" {
		t.Fatalf("a drag left the engine in %s", s.e.Mode())
	}
	s.keys("y")
	if got, _ := s.e.Register('"'); got != "half" {
		t.Errorf("the dragged area yanks %q, want %q", got, "half")
	}

	s.keys("A")
	s.e.Select(vim.Pos{Line: 1, Col: 0}, vim.Pos{Line: 1, Col: 5}, 'v')
	if s.e.Mode() != "visual" {
		t.Fatalf("a drag in Insert mode left the engine in %s", s.e.Mode())
	}
	s.e.Deselect()
	if s.e.Mode() != "normal" {
		t.Errorf("a click after a drag left the engine in %s", s.e.Mode())
	}
	s.keys("gv")
	if start, ok := s.e.Visual(); !ok || start != (vim.Pos{Line: 1, Col: 0}) || s.e.Cursor() != (vim.Pos{Line: 1, Col: 5}) {
		t.Errorf("gv after a click reselects %v→%v (%v)", start, s.e.Cursor(), ok)
	}

	// *<3-LeftMouse>*: "triple line".
	s.keys("<Esc>")
	s.e.Select(vim.Pos{Line: 0, Col: 3}, vim.Pos{Line: 0, Col: 3}, 'V')
	if s.e.Mode() != "visual-line" {
		t.Errorf("a triple click left the engine in %s", s.e.Mode())
	}
}

// scroll.txt: "If the cursor position is moved off of the window, the cursor is moved onto the window". The wheel moves
// the window, and the window stays where it was put when it then has fewer rows for text (blocks drawn between its
// lines took some): the cursor is moved onto it, rather than the window moved back to the cursor. A command that moves
// the cursor brings the window to it again.
func TestAScrolledWindowStaysWhenItsRowsShrink(t *testing.T) {
	dir := t.TempDir()
	var lines strings.Builder
	for i := range 100 {
		fmt.Fprintf(&lines, "line %d\n", i+1)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), lines.String())
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>")
	id := s.e.WindowID()
	s.e.SetRows(id, 20)
	s.keys("G")
	s.e.ScrollTo(id, 60)
	if s.e.Top() != 60 || s.e.Cursor().Line > 79 {
		t.Fatalf("the wheel left top %d, cursor %d", s.e.Top(), s.e.Cursor().Line)
	}
	s.e.SetRows(id, 15)
	if s.e.Top() != 60 {
		t.Errorf("fewer rows moved the scrolled window to %d, want it left at 60", s.e.Top())
	}
	if c := s.e.Cursor().Line; c < 60 || c >= 75 {
		t.Errorf("the cursor is on %d, off the window's 60 to 74", c)
	}
	s.keys("G")
	s.e.SetRows(id, 12)
	if bot := s.e.Top() + 12; s.e.Cursor().Line >= bot {
		t.Errorf("after G, fewer rows left the cursor on %d below the window (top %d)", s.e.Cursor().Line, s.e.Top())
	}
}
