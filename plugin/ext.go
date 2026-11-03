package plugin

import (
	"context"
	"encoding/json"
	"image"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// Plugin is a plugin in any tier; built-ins are plugins too.
type Plugin interface {
	Manifest() Manifest
	Activate(ctx context.Context, h Host) error
	Deactivate(ctx context.Context) error
}

// Host is what a plugin sees of the app: the registry, what's drawn included (uireg), and the app's own calls.
type Host interface {
	uireg.Registry
	Session(id core.SessionID) (core.Session, bool)
	Sessions() []core.Session
	Log() *slog.Logger
	Net() *http.Client // what plugins' HTTP goes through
	// Secrets are the named secrets the host attaches to a plugin's requests (http.request); a plugin never reads
	// them.
	Secrets() Secrets
	// Notify tells the user something, through the one path every notice takes. Source defaults to
	// the plugin's id and Time to now; the text is redacted before it's kept.
	Notify(n core.Notice)
	// Dismiss takes down the toasts of the notices with id: a problem that's gone. Their rows stay in the history.
	Dismiss(id string)
	State() core.State // as it stands, read-only: notification history, focus, sessions
	// Apply applies an event to the state, the one way to change it: core.SessionFocused, core.TabOpened, core.TabClosed.
	Apply(ev core.Event) error
	// Subscribe calls fn with every event the filter matches, from whichever goroutine applied it; Do gets back
	// to the loop. cancel stops it.
	Subscribe(f core.Filter, fn func(core.Event)) (cancel func())
	// Here is where the user is, for reading options: the session in front, the project, and the focused editor's
	// buffer and window.
	Here() registry.At
	// Layout is the tree every view is placed in. Change it only from commands and views, which run
	// on the app's loop.
	Layout() *layout.Root
	PaneRect(v layout.ViewRef) (render.Rect, bool) // where v's pane was drawn last frame
	PaneTab(id ui.ID) (layout.ViewRef, bool)       // the view whose tab holds node id, last frame
	Redraw()                                       // asks for a frame; safe from any goroutine
	Do(fn func())                                  // runs fn on the app's loop, before the next frame; safe from any goroutine
	Pending() Pending                              // keys typed towards a longer mapping or command, for which-key
	HoldPending()                                  // keeps the pending mapping waiting past keymap.timeoutlen, until the next key
	KeyModes() []string                            // the keymap modes a key goes through now, most specific first; nil with no window
	UI() UIView                                    // what the window shows: focus, help ids, right-clicks
	// ProvideUI gives the window's widgets a service of type t (ui.ProvideType), as ProvideUI[T] does; a later one
	// of a type shadows it until removed. Call it on the loop.
	ProvideUI(t reflect.Type, v any) (remove func())
	// WindowFocus is whether the window has the OS's focus, and a channel closed when that changes (FocusGained,
	// FocusLost): for work that slows while the window is behind another app. Safe from any goroutine.
	WindowFocus() (focused bool, changed <-chan struct{})
	// Kept is what Keep last kept for session under key, as JSON. Safe from any goroutine.
	Kept(session core.SessionID, key string) (json.RawMessage, bool)
	// Keep keeps v, JSON-encoded, for session under key (nil forgets it): the window holds it across reopens on
	// another directory and saves it across restarts, until the session is gone. Keys are the plugin's own:
	// "explorer", "session.opened". Safe from any goroutine.
	Keep(session core.SessionID, key string, v any) error
	// Claim holds key for the window until release; false while it's held already. Safe from any goroutine.
	Claim(key string) (release func(), ok bool)
	// Framed calls fn on the loop after each frame the window draws, until cancelled: for what a view wants only
	// while it's drawn.
	Framed(fn func()) (cancel func())
	CurrentTheme() *theme.Resolved // the theme the window is drawn in
	// Load starts p as a plugin of its own, on a host whose source is its id, as a plugin manager does; Dispose
	// stops it. Call it on the loop.
	Load(p Plugin) (registry.Disposable, error)
	// Plugins are the manifests of the plugins running now: the built-ins, then the ones started since. Call it on
	// the loop.
	Plugins() []Manifest
	// Shot is the window as it is now, built and drawn again on the CPU in th (its own theme when nil), in logical
	// pixels. A probe theme (theme.Probe) makes it a map of which token painted each pixel. Call it on the loop.
	Shot(th *theme.Resolved) *image.RGBA
}

// UIView is the window's widget tree as plugins may read it, last frame's.
type UIView interface {
	Focused() ui.ID
	Layered(z int) bool // last frame drew a float on layer z (ui.LayerPage: a full-window page is up)
	Help(id ui.ID) string
	Subject(id ui.ID) string // what the node stands for (ui.Style.Subject), from it up
	// SubjectAt is what was right-clicked at p stands for when target took the click: the node at p in it, else target.
	SubjectAt(target ui.ID, p render.Point) string
	HelpNodes() []ui.HelpNode
	ContextClick() (ui.ID, render.Point, bool)
	HelpRequest() (string, bool)
	Rect(id ui.ID) (render.Rect, bool)
	FocusNext(dir int) // moves keyboard focus to the next focusable node (back with -1), wrapping
	DragKey() bool     // picks up the focused node's drag, or drops the one held on it (ui/dnd.go)
	CancelDrag()
	TakesText() bool             // the focused node inserts typed text: a text field, an editor in insert mode
	Key(ev render.KeyEvent) bool // types a key at the focused node, past the keymaps, for its next frame
	Now() time.Time              // the window's clock: what the frames are drawn at, a test's fixed time
	Frames() int                 // how many frames have been built, to tell one frame from the next
}

// Pending is a key sequence waiting for more (registry.Pending).
type Pending = registry.Pending

// Hint is a key that can come next, and what it does (registry.Hint).
type Hint = registry.Hint

// ProvideUI gives h's window's widgets v as their service of type T (ui.Provide): the app's widget.Dictator.
func ProvideUI[T any](h Host, v T) registry.Disposable {
	return disposeFunc(h.ProvideUI(reflect.TypeFor[T](), v))
}
