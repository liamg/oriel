package host

import (
	"fmt"
	"runtime/debug"
)

// recover turns a panic in one of the host's entry points into an error, logged, so it can't take the window
// down. Deferred first in every exported method the app calls; err may be nil when there's nothing to return it in.
func (w *Host) recover(where string, err *error) {
	if r := recover(); r != nil {
		w.log.Error("panic in the session host", "in", where, "panic", r, "stack", string(debug.Stack()))
		if err != nil {
			*err = fmt.Errorf("the session host failed in %s: %v", where, r)
		}
	}
}

// goSafe runs fn on its own goroutine, recovering a panic in it.
func (w *Host) goSafe(where string, fn func()) {
	go func() {
		defer w.recover(where, nil)
		fn()
	}()
}
