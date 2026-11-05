package agentrun

import (
	"path"
	"sync"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/watch"
)

// eventsSettle is how long after a change the OS's file events have reached the manager: fswatch gathers them for
// fswatch.Delay, after FSEvents' own latency.
const eventsSettle = fswatch.Delay + 100*time.Millisecond

// treeWatch tells which of a session's projects have changed since they were last marked, from subscriptions to their
// work trees on the session's freshness manager, so a mark snapshots only those. With no manager, every project has
// always changed.
type treeWatch struct {
	m     *watch.Manager
	mu    sync.Mutex
	subs  map[string]watch.Sub[time.Time] // by project directory
	seen  map[string]bool                 // its first reading has come
	dirty map[string]bool                 // a reading since the project was last asked about
}

func newTreeWatch(m *watch.Manager) *treeWatch {
	if m == nil {
		return nil
	}
	return &treeWatch{m: m, subs: map[string]watch.Sub[time.Time]{}, seen: map[string]bool{}, dirty: map[string]bool{}}
}

// tree is a project's work tree as a source: a new reading each time its files change.
func tree(dir string) watch.Source[time.Time] {
	skip := func(rel string) bool { return rel == ".git" || path.Base(rel) == "node_modules" }
	return watch.Source[time.Time]{Key: "agentrun.tree:" + dir, Files: []watch.Files{{Path: dir, Tree: true, Skip: skip}},
		Read: func(watch.Reader) (time.Time, error) { return time.Now(), nil }}
}

// changed says whether dir may have changed since it was last asked about, and subscribes to it the first time.
func (t *treeWatch) changed(dir string) bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	s, ok := t.subs[dir]
	dirty := t.dirty[dir]
	t.dirty[dir] = false
	t.mu.Unlock()
	if !ok {
		s = watch.SubscribeFunc(t.m, tree(dir), watch.Need{Background: watch.Keep}, "the agent's shell commands' changes", func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.seen[dir] {
				t.dirty[dir] = true
			}
			t.seen[dir] = true
		})
		t.mu.Lock()
		t.subs[dir] = s
		t.mu.Unlock()
		return true
	}
	return dirty || s.Busy()
}

func (t *treeWatch) close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	subs := t.subs
	t.subs = map[string]watch.Sub[time.Time]{}
	t.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}
