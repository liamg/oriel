package watch

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/fswatch"
)

// fsRoots are the paths the file watcher was given, and the sources' Files they're for. A tree two sources watch
// leaves out only what both skip. Its own lock, not m.mu: the watcher asks skip while it holds its own.
type fsRoots struct {
	mu    sync.RWMutex
	files map[string][]Files // by path
}

func (r *fsRoots) skip(path string) func(rel string) bool {
	return func(rel string) bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		fs := r.files[path]
		for _, f := range fs {
			if !f.Tree || f.Skip == nil || !f.Skip(rel) {
				return false
			}
		}
		return len(fs) > 0
	}
}

// watch registers n's files with the file watcher, starting it the first time; false if it can't watch them all.
// m.mu is held.
func (m *Manager) watch(n *node) bool {
	if m.fw == nil && m.fwErr == nil {
		m.fw, m.fwErr = m.opts.Watch()
		if m.fwErr == nil {
			m.wg.Add(1)
			go m.events(m.fw)
		}
	}
	if m.fwErr != nil {
		return false
	}
	ok := true
	for _, f := range n.spec.files {
		p := filepath.Clean(f.Path)
		m.roots.mu.Lock()
		first := len(m.roots.files[p]) == 0
		m.roots.files[p] = append(m.roots.files[p], f)
		m.roots.mu.Unlock()
		n.watching = append(n.watching, p)
		if !first {
			continue
		}
		var err error
		if f.Tree {
			err = m.fw.AddTree(p, m.roots.skip(p))
		} else {
			err = m.fw.Add(p)
		}
		if err != nil {
			ok = false
		}
	}
	return ok
}

// unwatch takes n's files off the file watcher; m.mu is held.
func (m *Manager) unwatch(n *node) {
	for i, p := range n.watching {
		f := n.spec.files[i]
		m.roots.mu.Lock()
		fs := m.roots.files[p]
		for j := range fs {
			if fs[j].Path == f.Path && fs[j].Tree == f.Tree {
				fs = append(fs[:j:j], fs[j+1:]...)
				break
			}
		}
		if len(fs) == 0 {
			delete(m.roots.files, p)
		} else {
			m.roots.files[p] = fs
		}
		last := len(fs) == 0
		m.roots.mu.Unlock()
		if last && m.fw != nil {
			m.fw.Remove(p)
		}
	}
	n.watching = nil
}

// events marks stale the sources whose files changed.
func (m *Manager) events(fw *fswatch.Watcher) {
	defer m.wg.Done()
	for ev := range fw.Events() {
		m.mu.Lock()
		for _, n := range m.nodes {
			if !n.held || len(n.watching) == 0 {
				continue
			}
			for _, f := range n.spec.files {
				if !covers(f, ev.Path) {
					continue
				}
				if ev.Err != nil {
					n.fallback = true
				}
				n.stale([]string{ev.Path})
				break
			}
		}
		m.mu.Unlock()
	}
}

// covers says a change to p is one f watches: p itself, a folder's entry, or anything in a tree that isn't skipped.
func covers(f Files, p string) bool {
	root := filepath.Clean(f.Path)
	if p == root {
		return true
	}
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return false
	}
	rel := filepath.ToSlash(p[len(root)+1:])
	if !f.Tree {
		return !strings.Contains(rel, "/") // a folder's own entries
	}
	if f.Skip == nil {
		return true
	}
	for i := range len(rel) + 1 {
		if (i == len(rel) || rel[i] == '/') && f.Skip(rel[:i]) {
			return false
		}
	}
	return true
}
