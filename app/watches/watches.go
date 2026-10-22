// Package watches is the window's freshness managers (package watch): one per session that isn't archived, made and
// closed as sessions come and go, and the app's. They hear the window's focus, and are the registry's "watches"
// service.
package watches

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
	"github.com/liamg/oriel/watch/gitsrc"
)

// Watches makes and keeps the managers.
type Watches struct {
	kit.Disposer
	Timing watch.Timing // the managers'; the defaults when zero
	h      plugin.Host
	cache  *gitx.Cache
	wg     sync.WaitGroup

	mu       sync.Mutex
	app      *watch.Manager
	sessions map[core.SessionID]*watch.Manager
	trees    map[treeKey]*gitsrc.Tree
	named    map[string][]named // by name, the latest last
	next     int
}

type named struct {
	registry.Named
	id int
}

type treeKey struct {
	id   core.SessionID
	root string
}

func (x *Watches) Manifest() plugin.Manifest { return plugin.Manifest{ID: "watches"} }

func (x *Watches) Activate(ctx context.Context, h plugin.Host) error {
	x.h = h
	x.cache = gitx.NewCache()
	x.sessions, x.trees, x.named = map[core.SessionID]*watch.Manager{}, map[treeKey]*gitsrc.Tree{}, map[string][]named{}
	x.app = x.manager("app")
	x.sync(h.State())
	x.Add(kit.OnDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev.(type) {
		case core.SessionOpened, core.SessionArchived, core.SessionClosed:
			x.sync(h.State())
		}
	})))
	life := x.Lifetime()
	x.wg.Go(func() { x.follow(life) })
	for _, n := range x.builtins() {
		x.Add(x.Register(n))
	}
	x.Add(h.Contribute(registry.Service{Name: registry.WatchesService, Impl: x, Source: "builtin:watches"}))
	return nil
}

func (x *Watches) Deactivate(ctx context.Context) error {
	err := x.Disposer.Deactivate(ctx)
	x.wg.Wait()
	x.mu.Lock()
	ms := append([]*watch.Manager{x.app}, slices.Collect(maps.Values(x.sessions))...)
	x.sessions, x.trees = map[core.SessionID]*watch.Manager{}, map[treeKey]*gitsrc.Tree{}
	x.mu.Unlock()
	for _, m := range ms {
		m.Close()
	}
	x.cache.Close()
	return err
}

func (x *Watches) manager(name string) *watch.Manager {
	start := fswatch.New
	if w, ok := kit.WindowOf(x.h); ok {
		start = w.FSWatch
	}
	log := x.h.Log()
	m := watch.New(watch.Options{Name: name, Watch: start, Timing: x.Timing, Log: log.Debug, Polls: kit.PollsOf(x.h)})
	on, _ := x.h.WindowFocus()
	m.SetFocused(on)
	return m
}

// sync makes a manager for each session that isn't archived, and closes those of the rest.
func (x *Watches) sync(st core.State) {
	live := map[core.SessionID]bool{}
	for _, se := range st.Sessions {
		if !se.Archived {
			live[se.ID] = true
		}
	}
	x.mu.Lock()
	var gone []*watch.Manager
	for id, m := range x.sessions {
		if !live[id] {
			gone = append(gone, m)
			delete(x.sessions, id)
			for k := range x.trees {
				if k.id == id {
					delete(x.trees, k)
				}
			}
		}
	}
	for id := range live {
		if x.sessions[id] == nil {
			x.sessions[id] = x.manager(string(id))
		}
	}
	x.mu.Unlock()
	for _, m := range gone {
		m.Close()
	}
}

// follow hands the window's focus to every manager.
func (x *Watches) follow(ctx context.Context) {
	for {
		on, changed := x.h.WindowFocus()
		x.mu.Lock()
		ms := append([]*watch.Manager{x.app}, slices.Collect(maps.Values(x.sessions))...)
		x.mu.Unlock()
		for _, m := range ms {
			m.SetFocused(on)
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

func (x *Watches) For(id core.SessionID) *watch.Manager {
	x.mu.Lock()
	defer x.mu.Unlock()
	if id == "" {
		return x.app
	}
	return x.sessions[id]
}

func (x *Watches) App() *watch.Manager { return x.For("") }

func (x *Watches) Tree(id core.SessionID, root string) *gitsrc.Tree {
	k := treeKey{id, filepath.Clean(root)}
	x.mu.Lock()
	defer x.mu.Unlock()
	t := x.trees[k]
	if t == nil {
		t = gitsrc.New(k.root, x.cache)
		x.trees[k] = t
	}
	return t
}

func (x *Watches) Source(name string) (registry.Named, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ns := x.named[name]
	if len(ns) == 0 {
		return registry.Named{}, false
	}
	return ns[len(ns)-1].Named, true
}

func (x *Watches) Register(n registry.Named) registry.Disposable {
	x.mu.Lock()
	x.next++
	id := x.next
	x.named[n.Name] = append(x.named[n.Name], named{n, id})
	x.mu.Unlock()
	return kit.OnDispose(func() {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.named[n.Name] = slices.DeleteFunc(x.named[n.Name], func(e named) bool { return e.id == id })
		if len(x.named[n.Name]) == 0 {
			delete(x.named, n.Name)
		}
	})
}

func (x *Watches) Names() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return slices.Sorted(maps.Keys(x.named))
}
