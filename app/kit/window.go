package kit

import (
	"io/fs"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// The window's own services, which a plugin manager and an updater use.
const (
	ScriptsService  = "plugins.scripts"
	BuiltinsService = "plugins.builtin"
	QuitService     = "quit"
)

// ScriptOpts are where a script plugin's own files are and what it's granted.
type ScriptOpts struct {
	Dir    string         // its directory, where its processes and modules are (bin/…); none if ""
	Grants []plugin.Grant // what it may do; what its manifest declares if nil
	Data   string         // its data directory; plugins/<id> in the state directory if ""
	// Programs run its binaries as other programs: a bundled plugin's are oriel itself (plugin.BundledPrograms).
	Programs map[string][]string
}

// Scripts make Lua plugins from their files, as a plugin manager starts them.
type Scripts interface {
	// Script is the plugin whose oriel.toml and init.lua are at fsys's root.
	Script(fsys fs.FS, o ScriptOpts) plugin.Plugin
}

// Builtin is a Go plugin the window started with.
type Builtin struct {
	Manifest plugin.Manifest
	Native   bool // registered with plugin.Register, rather than one of the app's own
	Waits    bool // lazy, and not used yet
}

// Builtins are the Go plugins the window started with.
type Builtins interface {
	Builtins() []Builtin
}

// Quit is the window closing, its sessions' agents handed over first.
type Quit interface {
	// Quit closes the window once its agents are handed over; asked again while it waits, it doesn't wait.
	Quit()
	// Restart quits, then runs relaunch once the window has closed; a cancelled quit forgets it.
	Restart(relaunch func() error)
	// Draining is the hand-over a quit waits on, if one does. Call it on the loop.
	Draining() (Drain, bool)
}

// Drain is a quit waiting for its sessions' turns to end, then handing their agents over.
type Drain interface {
	Hide(hidden bool) // the quit dialog's, for a page that tells the hand-over itself
	Handing() bool    // the wait is over: the agents are stopping
	Busy() (busy []core.SessionState, polled bool)
	Deadline() time.Time // when it stops waiting
	Now()                // stops waiting
	Cancel()             // quits no more, unless it's handing over
}

func ScriptsOf(r registry.Registry) (Scripts, bool) {
	return registry.ServiceOf[Scripts](r, ScriptsService)
}
func BuiltinsOf(r registry.Registry) (Builtins, bool) {
	return registry.ServiceOf[Builtins](r, BuiltinsService)
}
func QuitOf(r registry.Registry) (Quit, bool) { return registry.ServiceOf[Quit](r, QuitService) }

// WindowService is the window itself: the directory it's on, and moving it onto another.
const WindowService = "window"

// Window is the directory the window is on, and moving it onto another: a session's, as it comes to the front. The
// plugins live on across a move; DirChanged says it happened.
type Window interface {
	// Dir is the directory the window is on; "" on the start view.
	Dir() string
	// Reopen moves the window onto dir, as session's ("" for none).
	Reopen(dir string, session core.SessionID) error
	// Hold keeps the window from laying out the directory it's moving onto, or starting on, until release: what that
	// directory's views need is being read (a StateFile). The opening cover stays up meanwhile; a hold not let go in
	// a couple of seconds is let go of. Release once, from any goroutine.
	Hold() (release func())
	// Arrived says the directory the window is on is laid out: nothing holds it any more, and the cover is down. A
	// view opened from now on stays.
	Arrived() bool
	// AfterReopen runs calls once the window has moved onto session's directory.
	AfterReopen(session core.SessionID, calls []Call)
	// LaunchedEmpty says, the first time it's asked, whether the window started with nothing open; false after.
	LaunchedEmpty() bool
	// Polls are the periods of the window's periodic work while it runs, for the idle budget.
	Polls() *PollSet
	// Frames are the frames the window has drawn lately.
	Frames() *FrameLog
	// FSWatch starts one of the OS's file watchers (fswatch.New, unless the config gives another).
	FSWatch() (*fswatch.Watcher, error)
}

// WindowDir is the directory r's window is on now ("" on the start view, or with no window). A plugin reads it when
// it needs it rather than keeping it: the window moves underneath the plugins (DirChanged).
func WindowDir(r registry.Registry) string {
	if w, ok := WindowOf(r); ok {
		return w.Dir()
	}
	return ""
}

// OnDirChanged runs fn on the loop each time the window moves onto another directory (DirChanged), for a plugin
// that keeps something per directory.
func OnDirChanged(r registry.Registry, label string, fn func(DirChanged)) registry.Disposable {
	return registry.On(r, registry.Hook{Kind: registry.Observe, Label: label}, func(_ registry.Ctx, ev *DirChanged) (registry.Verdict, error) {
		fn(*ev)
		return registry.Verdict{}, nil
	})
}

// PollsOf is r's window's periodic work; nil, noting nothing, without a window.
func PollsOf(r registry.Registry) *PollSet {
	if w, ok := WindowOf(r); ok {
		return w.Polls()
	}
	return nil
}

func WindowOf(r registry.Registry) (Window, bool) {
	return registry.ServiceOf[Window](r, WindowService)
}

// Moves says whether the window opens again on session id's directory as it comes to the front: one in another
// directory.
func Moves(h plugin.Host, id core.SessionID) bool {
	w, ok := WindowOf(h)
	se, has := h.Session(id)
	if !ok || !has {
		return false
	}
	dir := SessionDir(se.Snapshot())
	return dir != "" && filepath.Clean(dir) != filepath.Clean(w.Dir())
}

// After runs then once the window is on session id: after it opens again on the session's directory, if it does.
func After(h plugin.Host, id core.SessionID, then []Call) {
	if len(then) == 0 {
		return
	}
	if w, ok := WindowOf(h); ok && Moves(h, id) {
		w.AfterReopen(id, then)
		return
	}
	h.Do(func() { RunCalls(h, then) })
}

// Follow opens the window again on session id, when it's in another directory.
func Follow(h plugin.Host, id core.SessionID) {
	w, ok := WindowOf(h)
	if !ok || !Moves(h, id) {
		return
	}
	se, _ := h.Session(id)
	_ = w.Reopen(SessionDir(se.Snapshot()), id)
}
