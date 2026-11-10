package kit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// PathWatch calls changed, off the loop, when what it watches changes: a subscription to a files source in the app's
// freshness manager (registry.Watches), which watches through the OS, polls every poll when it can't, and reads
// nothing while the window is behind another app, catching up once it's back. A burst of changes is one call.
type PathWatch struct {
	changed func()
	m       *watch.Manager
	done    func() // closes m, when it's its own

	mu    sync.Mutex
	files []watch.Files
	sub   watch.Sub[[]string]
	gen   int
	kicks Busy // Kick's calls with nothing watched
}

// WatchPaths watches with r's app manager, polling every poll once it can't.
func WatchPaths(r registry.Registry, poll time.Duration, changed func()) *PathWatch {
	p := &PathWatch{changed: changed}
	p.m, p.done = FileManager(r, "paths", poll)
	return p
}

// Add watches a file, or a folder and its entries; it needn't exist yet.
func (p *PathWatch) Add(path string) { p.add(watch.Files{Path: path}) }

// AddTree watches a folder and everything under it but what skip leaves out.
func (p *PathWatch) AddTree(root string, skip func(rel string) bool) {
	p.add(watch.Files{Path: root, Tree: true, Skip: skip})
}

// add subscribes again with f among the files.
func (p *PathWatch) add(f watch.Files) {
	p.mu.Lock()
	p.files = append(p.files, f)
	p.gen++
	key := fmt.Sprintf("paths:%p:%d", p, p.gen)
	old := p.sub
	var read atomic.Bool
	p.sub = watch.SubscribeFunc(p.m, watch.FilesSource(key, p.files...), watch.Need{}, "files", func() {
		if read.Swap(true) {
			p.changed()
		} // its first reading is what's there now, not a change
	})
	p.mu.Unlock()
	if old.Valid() {
		old.Close()
	}
}

// Kick calls changed as though something watched changed.
func (p *PathWatch) Kick() {
	p.mu.Lock()
	sub := p.sub
	p.mu.Unlock()
	if sub.Valid() {
		sub.Refresh()
	} else {
		p.kicks.Go(p.changed)
	}
}

func (p *PathWatch) Close() {
	p.mu.Lock()
	sub := p.sub
	p.sub = watch.Sub[[]string]{}
	p.mu.Unlock()
	if sub.Valid() {
		sub.Close()
	}
	p.kicks.Wait()
	p.done()
}
