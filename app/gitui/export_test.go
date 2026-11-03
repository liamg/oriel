package gitui

import (
	"time"

	"github.com/liamg/oriel/gitx"
)

// What the package's outside tests read and set of its plugins.

func (g *Git) Wait()                 { g.wait() }
func (g *Git) Unread(dir string)     { g.unread(dir) }
func (x *Diff) Wait()                { x.wait() }
func (e *Explorer) Wait()            { e.wait() }
func (e *Explorer) Selected() string { return e.selected }

// SelectedFile is the file selected in root's diff view, if it's open.
func (x *Diff) SelectedFile(root string) (string, bool) {
	st := x.by[root]
	if st == nil {
		return "", false
	}
	return st.sel.file, true
}

// NewText is path's new text, as its diff was last made.
func (x *Diff) NewText(path string) ([]byte, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for k, d := range x.files {
		if k.path == path {
			return d.newText, true
		}
	}
	return nil, false
}

// Hunks are the hunks the Explorer lists: the file's, whether they've been read, and the unstaged ones.
func (e *Explorer) Hunks() (path string, loaded bool, unstaged []gitx.Hunk) {
	x := &e.hunks
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.path, x.loaded, x.unstaged
}

// SetLedgerTimes sets how long lines take to read, to skim, and each line read after the first. Call it on the loop.
func (x *Diff) SetLedgerTimes(read, skim, line time.Duration) {
	x.ledgerTimes.read, x.ledgerTimes.skim, x.ledgerTimes.line = read, skim, line
}

// The diff's word highlights.
var (
	TDiffAddWord = tDiffAddWord
	TDiffDelWord = tDiffDelWord
)

// Made is how many of the diff's files have been made, and whether one has been styled and blamed.
func (x *Diff) Made() (n int, done bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, d := range x.files {
		done = done || d.styled && d.blamed
	}
	return len(x.files), done
}

func (g *Git) Reload()       { g.reload() }
func (g *Git) Settled() bool { return g.settled() }

// Reveal asks root's diff view to bring file into view, as a key or diff.open does.
func (x *Diff) Reveal(root, file string) {
	st := x.state(root)
	st.sel = diffSel{file: file, hunk: -1, line: -1}
	st.reveal = true
}
