// Package hostcore is what the session host's parts share: the State each sees in place of the host, the Env they
// run in, and the types and small helpers more than one of them uses. It imports none of them.
package hostcore

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// State is the host's state as one of its parts sees it.
type State interface {
	// Snapshot is the whole state as it stands.
	Snapshot() core.State
	// Session is a copy of one session's state.
	Session(id core.SessionID) (core.SessionState, error)
	// Mutate runs fn on session id's state under the host's edit lock: one read-modify-write of a session at a time.
	Mutate(id core.SessionID, fn func(se core.SessionState) error) error
	// Emit applies an event.
	Emit(ev core.Event) error
}

// Restatus sets a session's status from what's in it.
func Restatus(st State, id core.SessionID) {
	se, err := st.Session(id)
	if err != nil {
		return
	}
	if s := core.DeriveStatus(se); s != se.Status {
		_ = st.Emit(core.SessionStatusChanged{ID: id, Status: s})
	}
}

// MutateThread changes a session's thread under the edit lock, so two changes to one thread can't race.
func MutateThread(st State, now time.Time, id core.SessionID, tid core.ThreadID, fn func(t *core.Thread) error) (core.Thread, error) {
	var out core.Thread
	err := st.Mutate(id, func(se core.SessionState) error {
		t, ok := se.Thread(tid)
		if !ok {
			return fmt.Errorf("no thread %s in %s", tid, id)
		}
		t.Comments = slices.Clone(t.Comments)
		t.Touched = slices.Clone(t.Touched)
		if err := fn(&t); err != nil {
			return err
		}
		t.Updated = now.UTC()
		if err := st.Emit(core.ThreadUpdated{Session: id, Thread: t}); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

// Kinds are the kinds of session the host starts.
type Kinds interface {
	Get(kind core.Kind) (core.KindSpec, bool)
	// Spec is se's kind's spec; a kind no longer registered can do everything a session with worktrees can.
	Spec(se core.SessionState) core.KindSpec
}

// FindThread resolves a thread id or a unique prefix of one, as an agent may give either.
func FindThread(se core.SessionState, ref string) (core.Thread, error) {
	ref = strings.TrimSpace(ref)
	var found []core.Thread
	for _, t := range se.Threads {
		if string(t.ID) == ref {
			return t, nil
		}
		if ref != "" && strings.HasPrefix(string(t.ID), ref) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return core.Thread{}, fmt.Errorf("no thread %q", ref)
	case 1:
		return found[0], nil
	}
	return core.Thread{}, fmt.Errorf("thread %q is ambiguous", ref)
}
