package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// Service is an object a plugin provides for other plugins to call, by name: the editors,
// git's work trees, the code sites, the ships, the sessions. A later one of a name shadows it until it's disposed.
type Service struct {
	Name   string
	Impl   any
	Source string
}

func (Service) ContributionKind() string   { return "service" }
func (s Service) ContributionName() string { return s.Name }

// ServiceOf is the service registered as name, if there is one and it's a T.
func ServiceOf[T any](r Contributions, name string) (T, bool) {
	for _, c := range r.Contributions("service") {
		if s := c.(Service); s.Name == name {
			t, ok := s.Impl.(T)
			return t, ok
		}
	}
	var zero T
	return zero, false
}

// The built-in services' names.
const (
	EditorsService   = "editors"
	GitTreesService  = "git.trees"
	CodeSitesService = "code.sites"
	ShipsService     = "ships"
	SessionsService  = "sessions"

	// The session host's parts' calls, each its own service (host.SessionCalls, host.SandboxCalls…).
	HostSessionsService  = "host.sessions"
	HostSandboxesService = "host.sandboxes"
	HostShipsService     = "host.ships"
	HostCodeHostsService = "host.codehosts"
	HostReposService     = "host.repos"
	HostJobsService      = "host.jobs"
	HostStatsService     = "host.stats"
)

// CodeSecretUnset is the JSON-RPC error a host request (http.request) answers when a secret it names isn't set:
// a plugin's process tests the code, not the message.
const CodeSecretUnset = -32010

// SecretUnset says err is a host request's answer that a secret it named isn't set: an error that knows its JSON-RPC
// code (jsonrpc.Coded) and says CodeSecretUnset.
func SecretUnset(err error) bool {
	var c interface{ RPCCode() int }
	return errors.As(err, &c) && c.RPCCode() == CodeSecretUnset
}

func EditorsOf(r Contributions) (Editors, bool)     { return ServiceOf[Editors](r, EditorsService) }
func GitTreesOf(r Contributions) (GitTrees, bool)   { return ServiceOf[GitTrees](r, GitTreesService) }
func CodeSitesOf(r Contributions) (CodeSites, bool) { return ServiceOf[CodeSites](r, CodeSitesService) }
func ShipsOf(r Contributions) (Ships, bool)         { return ServiceOf[Ships](r, ShipsService) }
func SessionsOf(r Contributions) (Sessions, bool)   { return ServiceOf[Sessions](r, SessionsService) }

// Sessions are the window's sessions.
type Sessions interface {
	Front() (core.SessionState, bool) // the session in front
	Get(id core.SessionID) (core.SessionState, bool)
	// FromArgs is the session a command's {id} names, else the one in front.
	FromArgs(a Args) (core.SessionState, error)
	// Start starts a session as session.start does with a, and answers its id.
	Start(ctx Ctx, a Args) (core.SessionID, error)
}

// SessionFromArgs is FromArgs over a session's id and the one in front: the shared body of Sessions.FromArgs.
func SessionFromArgs(a Args, front core.SessionID, get func(core.SessionID) (core.SessionState, bool)) (core.SessionState, error) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = front
	}
	if id == "" {
		return core.SessionState{}, errors.New("no session is in front")
	}
	se, ok := get(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %q", id)
	}
	return se, nil
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
	Window(id string) (EditorWindow, bool)
	Buffers() []EditorBuffer // the listed buffers that hold files
	Mode() string            // the current window's mode: "normal", "insert", "visual"…
	// Recording is the register vim's q is recording into, as typed ('A' appends to a).
	Recording() (rune, bool)
	// At is at with the current buffer and window, and the cursor's file and line, for reading options.
	At(at At) At
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
	WatchWindow(fn func(root string, paths []string)) (cancel func())
	// Scope is what the window's git views show: the Explorer's scope, Changed (the branch against its base),
	// Uncommitted (what isn't committed) or All. What marks changes elsewhere (the editor's gutter) follows it.
	Scope() gitx.Scope
}

// GitTree is one work tree's git status.
type GitTree interface {
	Root() string       // the repo's root; "" outside a repo, or until it's found
	State() *gitx.State // the last status read; nil outside a repo, or before the first
	// Counted is State with each file's line counts, read only while something keeps asking for them, frame to
	// frame; State until they're first read.
	Counted() *gitx.State
	Remote() string // origin's URL
	// Status is the repo's status as it's read and watched: its Repo for git's other calls; nil outside a repo.
	Status() *gitx.Status
	Rel(path string) string // a path relative to the repo's root, as git names it
	Load() Load             // how far its first read is
}

// Load is how far reading something is: Done once it's read (or there's nothing to read), Err while the read fails,
// and since when it's been waiting.
type Load struct {
	Done  bool
	Err   error
	Since time.Time
}

// CodeSites say which code host, and which of its sites, a remote or a pull request's URL is on.
// They don't block but Probe, and are safe off the loop.
type CodeSites interface {
	URL(raw string) (HostSite, bool)                             // a pull request's URL
	Remote(remote string) (HostSite, string, bool)               // a remote's URL, and the repo's name there
	Probe(ctx context.Context, hostname string) (HostSite, bool) // asks each host whether hostname is one of its
	Named(hostname string) (string, bool)                        // the code host the user, gh or glab names hostname as
	Remember(hostname, name, api string) error                   // keeps which host hostname is, and its API URL
	ReviewSources() []HostSite                                   // the sites review requests are read from
	Keyed(s HostSite) bool                                       // s's token is set
	Warm(ctx context.Context, s HostSite)                        // reads s's token ahead of its first request
	Refuse(s HostSite, err error) bool                           // says s refused its token with err; true if it was refused
	TrustedUser(login string) bool                               // code.trusted_users has login
}

// Ships are what each of a session's projects has to ship, as the Ship page last read them.
type Ships interface {
	// For are se's projects as last read; one not read yet has only its branch.
	For(se core.SessionState) []ProjectShip
	// Refresh reads id's projects again, fresh past what's cached.
	Refresh(id core.SessionID, fresh bool)
	// ResolveThread resolves a thread mirrored from a code host there, or reopens it.
	ResolveThread(se core.SessionState, t core.Thread, resolved bool) error
	// ForwardThread posts the user's forwarded draft on a mirrored thread to its host.
	ForwardThread(se core.SessionState, thread core.ThreadID, comment string) error
}

// ProjectShip is one of a session's projects as Ship read it.
type ProjectShip struct {
	Project  string
	ReadOnly bool
	Files    []string // uncommitted
	Unpushed int      // commits not pushed
	Commits  int      // on the branch since its base
	Pushed   bool
	PR       *PR
	Code     *CodeHost // where origin is; nil for none Oriel knows
	Load     Load
}
