package services

import (
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// QuitService is the window's Quit.
const QuitService = "quit"

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

func QuitOf(r registry.Contributions) (Quit, bool) { return registry.ServiceOf[Quit](r, QuitService) }
