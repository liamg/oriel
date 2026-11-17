package vim_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvim"
)

// windows.txt *:split_f*: "Like |:split| but create a new window and start editing file {file} in it. This behaves
// almost like a ":split" first, and then an ":edit" command, but the alternate file name in the original window is
// set to {file}." *:vsplit*: "Like |:split|, but split vertically."
func TestSplitWithAFileEditsItInTheNewWindow(t *testing.T) {
	for _, split := range []string{"split", "vsplit"} {
		t.Run(split, func(t *testing.T) {
			// After each step: the current buffer and how many windows. :e # in the original window edits its
			// alternate, which the split set to the file.
			steps := []struct{ keys, want string }{
				{":" + split + " b.txt<CR>", "b.txt 2"},
				{"<C-w>w", "a.txt 2"},
				{":e #<CR>", "b.txt 2"},
			}
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "a.txt"), "alpha\n")
			writeFile(t, filepath.Join(dir, "b.txt"), "bravo\n")
			s := newSession(t, dir)
			s.keys(":e a.txt<CR>")
			for _, st := range steps {
				s.keys(st.keys)
				if got := fmt.Sprintf("%s %d", s.e.Name(), s.e.Windows()); got != st.want {
					t.Errorf("after %s: %q, want %q", st.keys, got, st.want)
				}
			}
			if !vimtest.Oracle() {
				return
			}
			n, err := nvim.Start(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			if err := n.Exec("edit a.txt"); err != nil {
				t.Fatal(err)
			}
			for _, st := range steps {
				if err := n.Input(st.keys); err != nil {
					t.Fatal(err)
				}
				got, err := n.Eval(`bufname('%') . ' ' . winnr('$')`)
				if err != nil {
					t.Fatal(err)
				}
				if got != st.want {
					t.Errorf("nvim after %s: %q, want %q", st.keys, got, st.want)
				}
			}
		})
	}
}
