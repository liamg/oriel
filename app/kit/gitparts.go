package kit

import (
	"bytes"
	"os"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/theme"
)

// The git colours: a file's status letter and name, and the dots that say what's uncommitted; and a git panel's
// background.
var (
	TGitModified  = theme.Declare("git.modified", "amber")
	TGitAdded     = theme.Declare("git.added", "green")
	TGitDeleted   = theme.Declare("git.deleted", "red")
	TGitRenamed   = theme.Declare("git.renamed", "violet")
	TGitUntracked = theme.Declare("git.untracked", "fg.faint")
	TGitConflict  = theme.Declare("git.conflict", "red")
	TGitIgnored   = theme.Declare("git.ignored", "fg.ghost")
	TGitNameMod   = theme.Declare("git.name.modified", "accent")
	TGitDot       = theme.Declare("git.dot.uncommitted", "amber")
	TGitDotBoth   = theme.Declare("git.dot.both", "accent")
	TGitDotDone   = theme.Declare("git.dot.committed", "fg.faint")
	TExplorerBg   = theme.Declare("explorer.bg", "bg.surface")
)

// GitCodeTokens are the tokens a status letter is drawn in, and a file's name tinted with.
func GitCodeTokens(code byte) (letter, name theme.TokenID) {
	switch code {
	case 'M':
		return TGitModified, TGitNameMod
	case 'A':
		return TGitAdded, TGitAdded
	case 'D':
		return TGitDeleted, TGitDeleted
	case 'R', 'C':
		return TGitRenamed, TGitRenamed
	case '?':
		return TGitUntracked, TGitUntracked
	case 'U':
		return TGitConflict, TGitConflict
	}
	return 0, 0
}

// UncommittedCount is how many paths have changes not committed.
func UncommittedCount(st *gitx.State) int {
	seen := map[string]bool{}
	for _, f := range st.Files(gitx.Uncommitted) {
		seen[f.Path] = true
	}
	return len(seen)
}

// StatFresh is how long a file's size and time on disk are taken as read before they're read again.
const StatFresh = time.Second

// FileStat is a file's size and time on disk, and when they were read.
type FileStat struct {
	Size, MTime int64
	Read        time.Time
}

// StatNow is the file at path's stat, read now.
func StatNow(path string) FileStat {
	st := FileStat{Read: time.Now()}
	if fi, err := os.Stat(path); err == nil {
		st.Size, st.MTime = fi.Size(), fi.ModTime().UnixNano()
	}
	return st
}

// IsBinary says b looks like a binary file: a NUL in its first 8000 bytes, as git decides.
func IsBinary(b []byte) bool { return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 }
