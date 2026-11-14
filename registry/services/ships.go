package services

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// ShipsService is the window's Ships.
const ShipsService = "ships"

func ShipsOf(r registry.Contributions) (Ships, bool) {
	return registry.ServiceOf[Ships](r, ShipsService)
}

// Ships are what each of a session's projects has to ship, as the Ship page last read them.
type Ships interface {
	// For are se's projects as last read; one not read yet has only its branch.
	For(se core.SessionState) []ProjectShip
	// Refresh reads id's projects again, fresh past what's cached.
	Refresh(id core.SessionID, fresh bool)
	// ResolveThread starts resolving a thread mirrored from a code host there, or reopening it. The error is only
	// why it couldn't start; how it went shows on the project's step.
	ResolveThread(se core.SessionState, thread core.ThreadID, resolved bool) error
	// ForwardThread starts posting the user's draft reply, comment commentID, on a mirrored thread to its host. The
	// error is only why it couldn't start; how it went shows on the project's step.
	ForwardThread(se core.SessionState, thread core.ThreadID, commentID string) error
}

// ProjectShip is one of a session's projects as Ship read it.
type ProjectShip struct {
	Project  string
	ReadOnly bool
	Files    []string // uncommitted
	Unpushed int      // commits not pushed
	Commits  int      // on the branch since its base
	Pushed   bool
	PR       *registry.PR
	Code     *registry.CodeHost // where origin is; nil for none Oriel knows
	Load     registry.Load
}
