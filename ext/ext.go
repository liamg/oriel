package ext

import (
	"context"
	"log/slog"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// Extension is a plugin in any tier; built-ins are extensions too.
type Extension interface {
	Manifest() Manifest
	Activate(ctx context.Context, h Host) error
	Deactivate(ctx context.Context) error
}

type Manifest struct {
	ID string
}

// Host is what an extension sees of the app.
type Host interface {
	registry.Registry
	Session(id core.SessionID) (core.Session, bool)
	Sessions() []core.Session
	Log() *slog.Logger
	// Notify tells the user something, through the one path every notice takes (errors.md). Source defaults to
	// the extension's id and Time to now; the text is redacted before it's kept.
	Notify(n core.Notice)
	State() core.State // a copy: notification history, focus, sessions
	// Layout is the tree every view is placed in (ui.md 01d). Change it only from commands and views, which run
	// on the app's loop.
	Layout() *layout.Root
	PaneRect(v layout.ViewRef) (render.Rect, bool) // where v's pane was drawn last frame
	Redraw()                                       // asks for a frame; safe from any goroutine
	Pending() Pending                              // keys typed towards a longer mapping or command, for which-key
}

// Pending is a key sequence waiting for more: a mapping's (looked up in Modes), or the focused editor engine's
// own, with the keys that engine has next.
type Pending struct {
	Keys  []string // vim notation
	Modes []string
	Since time.Time
	Hints []Hint
}

// Hint is a key that can come next, and what it does.
type Hint struct {
	Keys, Desc string
}
