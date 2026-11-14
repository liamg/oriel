package services

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
	"github.com/liamg/oriel/watch/gitsrc"
)

// WatchesService is the window's freshness managers (Watches).
const WatchesService = "watches"

// Watches are the managers that keep readings fresh for whoever subscribes (package watch): one per session that
// isn't archived, and the app's, for what's no session's.
type Watches interface {
	// For is session id's manager; the app's for "", and nil for a session that's archived or gone.
	For(id core.SessionID) *watch.Manager
	App() *watch.Manager
	// Tree is the git sources of the work tree at root, as session id's manager (the app's for "") reads them.
	Tree(id core.SessionID, root string) *gitsrc.Tree
	// Sources are the sources plugins have registered by name (oriel.source), for subscribing by name.
	Source(name string) (Named, bool)
	// Register adds a named source; it's taken back when the returned Disposable is.
	Register(n Named) registry.Disposable
	Names() []string
}

// Named is a source subscribed to by name, with arguments: what oriel.subscribe asks for.
type Named struct {
	Name string
	Doc  string
	// Make is the source for args, read by session id's manager; its readings are plain values (strings, numbers,
	// tables) for Lua.
	Make func(id core.SessionID, args map[string]any) (watch.Source[any], error)
}

func WatchesOf(r registry.Contributions) (Watches, bool) {
	return registry.ServiceOf[Watches](r, WatchesService)
}
