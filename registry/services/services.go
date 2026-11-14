// Package services are the built-in services' contracts: the editors, git's work trees, the freshness managers, and
// the agent providers plugins bring. The registry carries them (registry.Service) without knowing them, so it stays
// free of what they're made of (syntax, gitx, watch, agent).
package services

import (
	"context"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// The built-in services' names.
const (
	EditorsService  = "editors"
	GitTreesService = "git.trees"
)

func EditorsOf(r registry.Contributions) (Editors, bool) {
	return registry.ServiceOf[Editors](r, EditorsService)
}

func GitTreesOf(r registry.Contributions) (GitTrees, bool) {
	return registry.ServiceOf[GitTrees](r, GitTreesService)
}

// Editors is the text editor as other plugins use it: its buffers, the one in front, and its own pending keys.
type Editors interface {
	Dir() string // the directory relative file names are under
	// Focused is the current window's buffer while an editor window has the focus.
	Focused() (EditorBuffer, bool)
	// Current is the current window's buffer, focused or not; false before there's one.
	Current() (EditorBuffer, bool)
	// Buffer is the listed buffer holding the file at path, if one does.
	Buffer(path string) (EditorBuffer, bool)
	// Window is the editor window id, or the current one for "".
	Window(id string) (registry.EditorWindow, bool)
	Buffers() []EditorBuffer // the listed buffers that hold files
	Mode() string            // the current window's mode: "normal", "insert", "visual"…
	// Recording is the register vim's q is recording into, as typed ('A' appends to a).
	Recording() (rune, bool)
	// At is at with the current buffer and window, and the cursor's file and line, for reading options.
	At(at registry.At) registry.At
	// Pending is the focused editor's engine waiting for more keys: vim's g, z or a count, emacs's C-x.
	Pending() Pending
	// Hints are the focused editor engine's own commands after keys a mapping is waiting on.
	Hints(keys []string) []Hint
	Shown() bool // an editor window is in the layout
	// Syntax is buf's parse, once the editor has made it.
	Syntax(buf *text.Buffer) (*syntax.Document, bool)
	SetCursor(pos text.Pos) // in the current window
	// JumpTo goes to pos in the file at path as a vim jump in the current window, so ⌃O comes back.
	JumpTo(path string, pos text.Pos) error
	// Edit makes rs in the current window's buffer as the user's edit: one undoable step.
	Edit(rs []text.Replace) error
	Cmdline() (string, bool) // the command line's text, while it's open
	// Selection is the current window's Visual selection, as a range of its text.
	Selection() (text.Range, bool)
	// Quickfix is the quickfix list (quickfix.set makes one): its title and its places.
	Quickfix() (title string, items []QuickfixItem)
}

// QuickfixItem is a place in the quickfix list.
type QuickfixItem struct {
	Path      string // as the list names it
	Line, Col int    // 1-based; Col 0 when there's none
	Text      string
}

// EditorBuffer is an editor buffer.
type EditorBuffer struct {
	Path     string // absolute; "" for one with no file
	Text     *text.Buffer
	Cursor   text.Pos // the current window's, for the current buffer
	Modified bool
	Bytes    func() []byte       // its text as writing it would put it on disk
	Encode   func([]byte) []byte // text as writing the buffer would put it on disk, in its file's format
}

// Pending is a key sequence waiting for more: a mapping's (looked up in Modes), or the focused editor engine's
// own, with the keys that engine has next.
type Pending struct {
	Keys  []string // vim notation
	Modes []string
	Since time.Time
	Hints []Hint
	Runs  string // what ⏎ runs now, when the keys are a mapping of their own as well as the start of longer ones
}

// Hint is a key that can come next, and what it does.
type Hint struct {
	Keys, Desc string
}

// GitTrees are the git status of the work trees the window shows, each read in the background and watched.
type GitTrees interface {
	// Tree is dir's work tree: the window's when dir is "" or in it, else one read from the first ask.
	Tree(dir string) GitTree
	// Holding is the work tree already read whose root holds path, else the window's.
	Holding(path string) GitTree
	// Run runs fn off the loop, then reads the window's tree again; an error is a notice.
	Run(fn func(ctx context.Context) error)
	Reload()    // reads every tree again
	Busy() bool // git work, or a tree's read, is running off the loop
	// WatchWindow calls fn with the window's work tree's root and the paths its status saw change, for whichever
	// directory the window is on: it carries on as the window moves, calling fn once with no paths when it's on
	// another tree (root "" outside a repo). fn is called off the loop, one call at a time, in order. Cancel stops it.
	WatchWindow(fn func(root string, paths []string)) registry.Disposable
	// Scope is what the window's git views show: the Explorer's scope, Changed (the branch against its base),
	// Uncommitted (what isn't committed) or All. What marks changes elsewhere (the editor's gutter) follows it.
	Scope() gitx.Scope
}

// GitTree is one work tree's git status.
type GitTree interface {
	Root() string       // the repo's root; "" outside a repo, or until it's found
	State() *gitx.State // the last status read; nil outside a repo, or before the first
	// StateWithCounts is State with each file's line counts, read only while something keeps asking for them, frame to
	// frame; State until they're first read.
	StateWithCounts() *gitx.State
	Remote() string // origin's URL
	// Status is the repo's status as it's read and watched: its Repo for git's other calls; nil outside a repo.
	Status() *gitx.Status
	Rel(path string) string // a path relative to the repo's root, as git names it
	Load() registry.Load    // how far its first read is
}
