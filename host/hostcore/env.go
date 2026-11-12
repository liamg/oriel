package hostcore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
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

// SessionKey names session id's processes apart from another Oriel's session of the same name: its directory, hashed.
func (e Env) SessionKey(id core.SessionID) string {
	sum := sha256.Sum256([]byte(e.SessionDir(id)))
	return hex.EncodeToString(sum[:8])
}

// AgentEnv is what an agent of session id runs with on top of its own environment, which every process it starts
// inherits: the session, and its key, by which what's left running is found when the session ends.
func (e Env) AgentEnv(id core.SessionID) []string {
	return []string{sysload.SessionEnv + "=" + string(id), sysload.SessionKeyEnv + "=" + e.SessionKey(id)}
}

// RetiredID is the mark a deleted session leaves: while it's there no session started later takes its id, so
// nothing kept by id (in the app, in an agent's own logs) turns up on another session.
func (e Env) RetiredID(id core.SessionID) string {
	return filepath.Join(e.Home, "retired", string(id))
}

// Retire marks id as a deleted session's, never to be another's.
func (e Env) Retire(id core.SessionID) error {
	p := e.RetiredID(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o600)
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
