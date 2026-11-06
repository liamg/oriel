package gitui

import (
	"slices"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/watch"
	"github.com/liamg/oriel/watch/gitsrc"
)

// parkedMost is how many work trees the window has left keep their status, for coming back to.
const parkedMost = 8

// parkedTree is a work tree the window has left: its reads paused, its last status kept.
type parkedTree struct {
	src     *gitsrc.Tree
	status  *gitx.Status
	session core.SessionID
	sub     watch.Sub[*gitx.State]
	remote  watch.Sub[string]
	setAt   time.Time
}

func (p *parkedTree) close() {
	p.sub.Close()
	p.remote.Close()
}

// park keeps the window's tree as the window leaves it, its reads paused and its status as last read, so coming
// back shows it at once.
func (g *Git) park() {
	t := &g.gitTree
	if t.src == nil {
		return
	}
	t.mu.Lock()
	p := &parkedTree{src: t.src, status: t.status, session: t.session, sub: t.sub, remote: t.remote, setAt: t.setAt}
	if !p.sub.Valid() {
		t.mu.Unlock()
		return
	}
	t.sub, t.remote = watch.Sub[*gitx.State]{}, watch.Sub[string]{}
	t.mu.Unlock()
	p.sub.OnChange(func() {})
	p.remote.OnChange(func() {})
	p.sub.Pause()
	p.remote.Pause()
	root := p.src.Root
	g.mu.Lock()
	defer g.mu.Unlock()
	if old := g.parked[root]; old != nil {
		old.close()
	}
	g.parked[root] = p
	g.parkOrder = append(slices.DeleteFunc(g.parkOrder, func(r string) bool { return r == root }), root)
	for len(g.parkOrder) > parkedMost {
		g.parked[g.parkOrder[0]].close()
		delete(g.parked, g.parkOrder[0])
		g.parkOrder = g.parkOrder[1:]
	}
}

// unpark takes root's parked tree back into the window's, reading it again: true when there was one.
func (g *Git) unpark(root string) bool {
	g.mu.Lock()
	p := g.parked[root]
	delete(g.parked, root)
	g.parkOrder = slices.DeleteFunc(g.parkOrder, func(r string) bool { return r == root })
	g.mu.Unlock()
	if p == nil {
		return false
	}
	t, h := &g.gitTree, g.h
	t.src, t.status = p.src, p.status
	t.mu.Lock()
	t.session, t.sub, t.remote, t.setAt = p.session, p.sub, p.remote, p.setAt
	t.mu.Unlock()
	p.sub.OnChange(func() {
		t.sync(p.sub, p.status)
		h.Redraw()
	})
	p.remote.OnChange(h.Redraw)
	t.sync(p.sub, p.status) // the reading kept, already its status's: nothing to hand over
	p.sub.Resume()          // its watches kept on while it was away: it reads again only if something changed
	p.remote.Resume()
	return true
}

// closeParked lets go of every parked tree.
func (g *Git) closeParked() {
	g.mu.Lock()
	ps := g.parked
	g.parked, g.parkOrder = map[string]*parkedTree{}, nil
	g.mu.Unlock()
	for _, p := range ps {
		p.close()
	}
}

// knownRoot is the root of the work tree dir is in, as found when the window was last on it: only a root found is
// kept, so a directory made a repo since is looked at again.
func (g *Git) knownRoot(dir string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, ok := g.roots[dir]
	return root, ok
}

func (g *Git) rememberRoot(dir, root string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.roots[dir] = root
}
