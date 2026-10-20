package vim_test

import (
	"path/filepath"
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
