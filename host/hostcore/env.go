package hostcore

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/liamg/oriel/core"
)

// Env is what each part of the host runs in: its home, its clock and log, and when it closes.
type Env struct {
	Home   string // ~/.oriel
	Log    *slog.Logger
	Now    func() time.Time
	Closed <-chan struct{}
	Work   *Jobs // what Go starts, which the host waits for as it closes
}

// Recover turns a panic into an error, logged, so it can't take the window down. Deferred first in every entry point
// the app calls; err may be nil when there's nothing to return it in.
func (e Env) Recover(where string, err *error) {
	if r := recover(); r != nil {
		e.Log.Error("panic in the session host", "in", where, "panic", r, "stack", string(debug.Stack()))
		if err != nil {
			*err = fmt.Errorf("the session host failed in %s: %v", where, r)
		}
	}
}

// Go runs fn on its own goroutine, recovering a panic in it; the host's Close waits for it.
func (e Env) Go(where string, fn func()) {
	run := func() {
		defer e.Recover(where, nil)
		fn()
	}
	if e.Work == nil {
		go run()
		return
	}
	e.Work.Go(where, run)
}

// SessionDir is where a session's log and files are kept.
func (e Env) SessionDir(id core.SessionID) string {
	return filepath.Join(e.Home, "sessions", string(id))
}

// WorkspacesDir is where the workspaces Oriel makes go.
func (e Env) WorkspacesDir() string { return filepath.Join(e.Home, "workspaces") }

// IsClosed says the host has closed.
func (e Env) IsClosed() bool {
	select {
	case <-e.Closed:
		return true
	default:
		return false
	}
}
