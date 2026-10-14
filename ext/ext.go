package ext

import (
	"context"
	"image"
	"log/slog"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
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
	// Dismiss takes down the toasts of the notices with id: a problem that's gone. Their rows stay in the history.
	Dismiss(id string)
	State() core.State // a copy: notification history, focus, sessions
	// Subscribe calls fn with every event the filter matches, from whichever goroutine applied it; Do gets back
	// to the loop. cancel stops it.
	Subscribe(f core.Filter, fn func(core.Event)) (cancel func())
	// Here is where the user is, for reading options: the session in front, the project, and the focused editor's
	// buffer and window.
	Here() registry.At
	// Layout is the tree every view is placed in (ui.md 01d). Change it only from commands and views, which run
	// on the app's loop.
	Layout() *layout.Root
	PaneRect(v layout.ViewRef) (render.Rect, bool) // where v's pane was drawn last frame
	Redraw()                                       // asks for a frame; safe from any goroutine
	Do(fn func())                                  // runs fn on the app's loop, before the next frame; safe from any goroutine
	Pending() Pending                              // keys typed towards a longer mapping or command, for which-key
	HoldPending()                                  // keeps the pending mapping waiting past keymap.timeoutlen, until the next key
	UI() UIView                                    // what the window shows: focus, help ids, right-clicks
	CurrentTheme() *theme.Resolved                 // the theme the window is drawn in
	// Shot is the window as it is now, built and drawn again on the CPU in th (its own theme when nil), in logical
	// pixels. A probe theme (theme.Probe) makes it a map of which token painted each pixel. Call it on the loop.
	Shot(th *theme.Resolved) *image.RGBA
}

// UIView is the window's widget tree as extensions may read it, last frame's.
type UIView interface {
	Focused() ui.ID
	Layered(z int) bool // last frame drew a float on layer z (ui.LayerPage: a full-window page is up)
	Help(id ui.ID) string
	HelpNodes() []ui.HelpNode
	ContextClick() (ui.ID, render.Point, bool)
	HelpRequest() (string, bool)
	Rect(id ui.ID) (render.Rect, bool)
	FocusNext(dir int) // moves keyboard focus to the next focusable node (back with -1), wrapping
	DragKey() bool     // picks up the focused node's drag, or drops the one held on it (ui/dnd.go)
	CancelDrag()
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
