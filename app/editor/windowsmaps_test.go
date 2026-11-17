package editor_test

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/render/headless"
)

// :nnoremap, :nunmap and :nmapclear in the editor are keymaps of the IDE's: a mapped key types its right-hand side,
// an unmapped one is gone, and unmapping one that isn't mapped says so. What each step leaves was read off headless
// nvim 0.12 with the same commands.
//
// map.txt *:nnoremap*: "Map the key sequence {lhs} to {rhs} for the modes where the map command applies.  Disallow
// mapping of {rhs}, to avoid nested and recursive mappings."
// map.txt *:nunmap*: "Remove the mapping of {lhs} for the modes where the map command applies."
// map.txt *:mapclear*: "Remove ALL mappings for the modes where the map command applies."
func TestEditorMapsAreKeymaps(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\ntwo\nthree\n"}, []string{"a.txt"})
	r.WaitText("three")
	r.TypeKeys(`:nnoremap \ dd<CR>`)
	r.TypeKeys(`\:w<CR>`)
	r.waitFile("a.txt", "two\nthree\n")

	r.TypeKeys(`:nunmap \<CR>`)
	r.TypeKeys(`\:w<CR>`)
	r.Drain()
	r.waitFile("a.txt", "two\nthree\n")
	r.TypeKeys(`:nunmap \<CR>`)
	if n := r.waitNotice("vim.no-mapping"); !strings.Contains(n.Text, "No such mapping") {
		t.Errorf("unmapping again: %+v", n)
	}

	r.TypeKeys(`:nnoremap \ dd<CR>:nnoremap <lt>Bar> x<CR>:nmapclear<CR>`)
	r.TypeKeys(`\|:w<CR>`)
	r.Drain()
	r.waitFile("a.txt", "two\nthree\n")
}

// CTRL-W x, CTRL-W r and CTRL-W o move and close the panes of the IDE's layout as nvim moves its windows, and :qa
// closes the editor's panes, not Oriel. Each step's order, left to right, and the window the cursor is in were read
// off headless nvim 0.12 doing the same.
//
// windows.txt *CTRL-W_x*: "Without count: Exchange current window with next one. ... The cursor is put in the other
// window."
// windows.txt *CTRL-W_r*: "Rotate windows downwards/rightwards.  The first window becomes the second one, the second
// one becomes the third one, etc.  The last window becomes the first window.  The cursor remains in the same window."
// windows.txt *CTRL-W_o*: "Make the current window the only one on the screen.  All other windows are closed."
// editing.txt *:qa*: "Exit Vim, unless there are some buffers which have been changed."
func TestEditorMovesItsWindowsAsPanes(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "alpha\n", "b.txt": "bravo\n", "c.txt": "charlie\n"}, []string{"a.txt"})
	r.WaitText("alpha")
	steps := []struct {
		keys  string
		order []string
		in    string
	}{
		{":vsplit b.txt<CR>", []string{"bravo", "alpha"}, "b.txt"},
		{":vsplit c.txt<CR>", []string{"charlie", "bravo", "alpha"}, "c.txt"},
		{"<C-w>x", []string{"bravo", "charlie", "alpha"}, "b.txt"},
		{"<C-w>r", []string{"alpha", "bravo", "charlie"}, "b.txt"},
		{"<C-w>o", []string{"bravo"}, "b.txt"},
	}
	for _, s := range steps {
		r.TypeKeys(s.keys)
		waitPanes(t, r, s.keys, s.order)
		r.WaitFor("the cursor in "+s.in, func() bool { return filepath.Base(r.Host.Here().File) == s.in })
		if n := len(r.shown()); n != len(s.order) {
			t.Fatalf("after %s the layout shows %d panes, want %d", s.keys, n, len(s.order))
		}
	}
	r.TypeKeys(":qa<CR>")
	r.WaitFrame("the editor's panes closed", func(s string) bool { return !strings.Contains(s, "bravo") && !strings.Contains(s, "NORMAL") })
}

// waitPanes waits for a frame whose panes show want's words left to right, failing with the last order seen.
func waitPanes(t *testing.T, r *running, after string, want []string) {
	t.Helper()
	words := []string{"alpha", "bravo", "charlie"}
	var got []string
	ctx, cancel := context.WithTimeout(context.Background(), apptest.Timeout)
	defer cancel()
	_, err := r.Win.WaitFrame(ctx, func(f headless.Frame) bool {
		x := map[string]float32{}
		for _, run := range f.Runs() {
			if slices.Contains(words, run.Text) && run.Visible() {
				if at, ok := x[run.Text]; !ok || run.Shown.Min.X < at {
					x[run.Text] = run.Shown.Min.X
				}
			}
		}
		got = got[:0]
		for w := range x {
			got = append(got, w)
		}
		slices.SortFunc(got, func(a, b string) int { return cmp.Compare(x[a], x[b]) })
		return slices.Equal(got, want)
	})
	if err != nil {
		fs := r.Win.Frames()
		t.Fatalf("after %s the panes are %v, want %v; last frame:\n%s", after, got, want, fs[len(fs)-1].Text())
	}
}
