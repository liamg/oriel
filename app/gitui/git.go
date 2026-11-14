package gitui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
	"github.com/liamg/oriel/watch/gitsrc"
)

// treeNeed is how fresh a work tree's status is kept: read once its changes settle (a burst at most a second), and
// not while the window is behind another app.
var treeNeed = watch.Need{}

// Git is the work trees' git status for the explorer, the gutters and the pickers, and the git.* commands that change
// it. Each tree's status is a subscription to its session's manager (services.Watches): the work dir's for as long as
// the plugin runs, others' for as long as something keeps using them, frame to frame. Outside a repo a tree has no
// status and the commands say so.
type Git struct {
	kit.Disposer
	gitTree                   // the work dir's
	scope   func() gitx.Scope // the Explorer's, while it's active: GitTrees.Scope
	h       plugin.Host
	links   kit.Links // the dirs asked about, resolved
	busy    kit.Busy
	running atomic.Int32 // the work busy waits on

	mu        sync.Mutex
	others    map[string]*gitTree    // by the directory asked for
	parked    map[string]*parkedTree // the work trees the window has left, by root
	parkOrder []string               // parked's roots, the longest left first
	roots     map[string]string      // the root of each directory the window has been on: "" outside a repo
	nests     map[string]bool        // by root and directory: whether each directory asked about is in a repo of its own under the window's
	// watchers follow the window's work tree (WatchWindow); unwatch stops its status calling them.
	watchers map[int]func(root string, paths []string)
	nextW    int
	unwatch  func()
	told     kit.Serial // the watchers' calls, off the loop

	ask *commitAsk // the commit dialog, while it's open
}

// gitTree is one work tree's status, and its remote; status is nil outside a repo, or until it's found.
type gitTree struct {
	g      *Git
	status *gitx.Status
	src    *gitsrc.Tree
	work   string    // the directory paths not absolute are relative to
	ready  bool      // its root has been looked for
	asked  time.Time // when it was first asked for
	held   bool      // the work dir's: subscribed for good, not by use

	syncMu  sync.Mutex // held handing status a reading, so a later one is never replaced by an earlier
	mu      sync.Mutex
	session core.SessionID // whose manager reads it
	sub     watch.Sub[*gitx.State]
	remote  watch.Sub[string]
	counts  watch.Sub[*gitx.State]           // while something shows line counts
	setAt   time.Time                        // when the reading last handed to status was read
	merged  struct{ st, c, out *gitx.State } // the status with the counts last read
	used    bool                             // since the last frame
	counted bool                             // line counts asked for since the last frame
	unread  bool                             // a test's: no reads until Reload
	failed  error
}

func (g *Git) Manifest() plugin.Manifest { return plugin.Manifest{ID: "git"} }

func (g *Git) Activate(ctx context.Context, h plugin.Host) error {
	g.Add(h.Contribute(registry.Busy{Name: "git", Busy: func() bool { return gitTrees{g}.Busy() || g.busy.Busy() || g.told.Busy() }, Source: "builtin:git"}))
	g.h = h
	g.others, g.parked, g.roots = map[string]*gitTree{}, map[string]*parkedTree{}, map[string]string{}
	g.gitTree = gitTree{g: g, held: true}
	g.Add(h.Contribute(registry.Service{Name: services.GitTreesService, Impl: gitTrees{g}, Source: "builtin:git"}))
	g.watchers = map[int]func(string, []string){}
	if root, ok := g.findRoot(ctx, h.Here().Project); ok {
		g.rememberRoot(h.Here().Project, root)
		g.hold(root)
	}
	g.Add(registry.DisposeFunc(func() { g.release() }))
	g.Add(registry.DisposeFunc(g.closeParked))
	g.Add(registry.DisposeFunc(g.told.Stop))
	g.Add(kit.OnDirChanged(h, "git's work tree", func(kit.DirChanged) { g.follow() }))
	for _, c := range g.commands() {
		g.Add(h.Command(c))
	}
	g.activateCommit(h)
	g.Add(h.Framed(g.framed))
	return nil
}

// findRoot is the root of the work tree dir is in, and the window's tree set up for it.
func (g *Git) findRoot(ctx context.Context, dir string) (string, bool) {
	g.gitTree.reset(dir, g.now())
	g.ready = true
	if dir == "" {
		return "", false
	}
	root, err := gitx.Root(ctx, dir)
	return root, err == nil
}

// hold reads the work tree at root as the window's, for good: Tree("") is it until the window moves (DirChanged).
// Its changes redraw and go to the WatchWindow watchers.
func (g *Git) hold(root string) {
	if !g.unpark(root) {
		g.gitTree.found(root)
	}
	g.unwatch = g.status.Watch(func(paths []string) {
		g.h.Redraw()
		g.notify(root, paths)
	})
	g.gitTree.subscribe()
}

// release lets go of the window's work tree.
func (g *Git) release() {
	g.gitTree.unsubscribe()
	if g.unwatch != nil {
		g.unwatch()
		g.unwatch = nil
	}
}

// follow moves the window's work tree onto the directory it's moved to. git finds its root off the loop: until it
// has, the window's tree is none, and its watchers hear of the new one once it's held.
func (g *Git) follow() {
	g.park()
	g.release()
	dir := g.h.Here().Project
	g.gitTree.reset(dir, g.now())
	if dir == "" {
		g.gitTree.ready = true
		g.notify("", nil)
		return
	}
	if root, ok := g.knownRoot(dir); ok { // been here: its root, and its status if it's parked, are at hand
		g.hold(root)
		g.ready = true
		g.notify(g.root(), nil)
		g.h.Redraw()
		return
	}
	g.running.Add(1)
	g.busy.Go(func() {
		root, rootErr := gitx.Root(context.Background(), dir)
		ok := rootErr == nil
		g.h.Do(func() {
			defer g.running.Add(-1)
			if g.work != dir { // moved on again while it looked
				return
			}
			if ok {
				g.rememberRoot(dir, root)
				g.hold(root)
			}
			g.ready = true
			g.notify(g.root(), nil)
			g.h.Redraw()
		})
	})
}

// notify hands the WatchWindow watchers a change to the window's work tree at root.
func (g *Git) notify(root string, paths []string) {
	g.mu.Lock()
	fns := make([]func(string, []string), 0, len(g.watchers))
	for _, fn := range g.watchers {
		fns = append(fns, fn)
	}
	g.mu.Unlock()
	// Off the loop and in order: a status is often set in a frame's build, and a watcher reads files.
	g.told.Queue(func() {
		for _, fn := range fns {
			fn(root, paths)
		}
	})
}

// watchWindow adds a WatchWindow watcher.
func (g *Git) watchWindow(fn func(root string, paths []string)) func() {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.nextW
	g.nextW++
	g.watchers[id] = fn
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		delete(g.watchers, id)
	}
}

func (g *Git) commands() []registry.Command {
	h := g.h
	paths := func(a registry.Args) []string {
		var ps []string
		switch v := a["path"].(type) {
		case string:
			ps = append(ps, v)
		case []string:
			ps = v
		case []any:
			for _, p := range v {
				if s, ok := p.(string); ok {
					ps = append(ps, s)
				}
			}
		}
		return ps
	}
	inRepo := func(registry.Args) bool { return g.status != nil }
	op := func(fn func(ctx context.Context, r gitx.Repo, ps []string) error) func(registry.Ctx, registry.Args) error {
		return func(_ registry.Ctx, a registry.Args) error {
			ps := paths(a)
			if len(ps) == 0 {
				return fmt.Errorf("no path")
			}
			t := g.holding(ps[0])
			if t.status == nil {
				return fmt.Errorf("not in a git repository")
			}
			for i, p := range ps {
				ps[i] = t.Rel(p)
			}
			g.async(func(ctx context.Context) error { return fn(ctx, t.status.Repo(), ps) }, t)
			return nil
		}
	}
	return []registry.Command{
		{ID: "git.stage", Title: "Stage files: {path}", Available: inRepo, Run: op(func(ctx context.Context, r gitx.Repo, ps []string) error {
			return r.Stage(ctx, ps...)
		})},
		{ID: "git.unstage", Title: "Unstage files: {path}", Available: inRepo, Run: op(func(ctx context.Context, r gitx.Repo, ps []string) error {
			return r.Unstage(ctx, ps...)
		})},
		{ID: "git.discard", Title: "Throw away files' unstaged changes, after asking: {path}", Available: inRepo,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				if a["confirm"] != true {
					ps := paths(a)
					for i, p := range ps {
						ps[i] = g.holding(p).Rel(p)
					}
					h.Notify(core.Notice{ID: "git.discard", Severity: core.SevQuestion, Confirm: true, Title: "Discard changes?",
						Text: "Throw away the unstaged changes to " + strings.Join(ps, ", ") + "? Untracked files are deleted.",
						Actions: []core.Action{
							{Title: "Discard", Command: "git.discard", Args: map[string]any{"path": ps, "confirm": true}},
							{Title: "Keep", Command: "notice.dismiss", Args: map[string]any{"id": "git.discard"}},
						}})
					return nil
				}
				h.Dismiss("git.discard")
				return op(func(ctx context.Context, r gitx.Repo, ps []string) error { return r.Discard(ctx, ps...) })(ctx, a)
			}},
		{ID: "git.refresh", Title: "Read the work tree's git status again", Available: inRepo, Run: func(registry.Ctx, registry.Args) error {
			g.async(func(context.Context) error { return nil })
			return nil
		}},
	}
}

// framed lets go of the trees, and the line counts, nothing used since the last frame.
func (g *Git) framed() {
	for _, t := range append(g.trees(), &g.gitTree) {
		t.mu.Lock()
		used, counted, counts := t.used, t.counted, t.counts
		t.used, t.counted = false, false
		if !counted {
			t.counts = watch.Sub[*gitx.State]{}
		}
		t.mu.Unlock()
		if !counted && counts.Valid() {
			counts.Close()
		}
		if !used && !t.held {
			t.unsubscribe()
		}
	}
}

// trees are the work trees the window reads other than its own.
func (g *Git) trees() []*gitTree {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*gitTree, 0, len(g.others))
	for _, t := range g.others {
		out = append(out, t)
	}
	return out
}

// tree is dir's work tree: the work dir's when dir is in it, else one whose root is looked for from the first ask.
// Until its root is found it has no status. A dir under the window's root is the window's own tree unless a work
// tree of its own sits between them, which is looked for the same way, off the loop: until then it's a tree whose
// root isn't found yet, never the wrong one.
func (g *Git) tree(dir string) *gitTree {
	if dir == "" || filepath.Clean(dir) == filepath.Clean(g.work) {
		return &g.gitTree
	}
	under := g.root() != "" && g.links.Within(dir, g.root())
	if under {
		if n, known := g.nested(dir); known && !n {
			return &g.gitTree
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.others[dir]; ok {
		return t
	}
	t := &gitTree{g: g, work: dir, asked: g.now()}
	g.others[dir] = t
	g.running.Add(1)
	g.busy.Go(func() {
		root, rootErr := gitx.Root(context.Background(), dir)
		ok := rootErr == nil
		// The loop reads the tree's fields unlocked, so they're set there; Busy holds until they are.
		g.h.Do(func() {
			defer g.running.Add(-1)
			g.mu.Lock()
			own := under && (!ok || filepath.Clean(root) == filepath.Clean(g.root()))
			if under {
				g.setNested(dir, !own)
			}
			if own {
				delete(g.others, dir) // the window's own tree: it's asked for that from now on
				t.ready = true
				g.mu.Unlock()
				g.h.Redraw()
				return
			}
			if ok {
				t.found(root)
			}
			t.ready = true
			g.mu.Unlock()
			if ok {
				t.status.Watch(func([]string) { g.h.Redraw() })
			}
		})
		g.h.Redraw()
	})
	return t
}

// nested says whether dir is in a work tree of its own under the window's root, once tree has looked.
func (g *Git) nested(dir string) (nested, known bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nests[filepath.Clean(g.root())+"\x00"+filepath.Clean(dir)]
	return n, ok
}

// setNested keeps what tree found of dir; g.mu is held.
func (g *Git) setNested(dir string, n bool) {
	if g.nests == nil {
		g.nests = map[string]bool{}
	}
	g.nests[filepath.Clean(g.root())+"\x00"+filepath.Clean(dir)] = n
}

// holding is the work tree a path is in: one already found whose root holds it, else the work dir's.
func (g *Git) holding(p string) *gitTree {
	if filepath.IsAbs(p) {
		for _, t := range g.trees() {
			if t.Root() != "" && kit.Within(p, t.Root()) {
				return t
			}
		}
	}
	return &g.gitTree
}

func (g *Git) Deactivate(ctx context.Context) error {
	plugin.Teardown(ctx, func(context.Context) { g.busy.Wait() })
	err := g.Disposer.Deactivate(ctx)
	for _, t := range append(g.trees(), &g.gitTree) {
		t.unsubscribe()
	}
	return err
}

// async runs fn off the loop, then has the trees it changed read again (every tree when none are named); an error is
// a notice. Other trees' watches stand: an op on one tree changes no other.
func (g *Git) async(fn func(ctx context.Context) error, trees ...*gitTree) {
	g.running.Add(1)
	g.busy.Go(func() {
		defer g.running.Add(-1)
		if err := fn(context.Background()); err != nil {
			g.h.Notify(core.Notice{ID: "git.error", Severity: core.SevError, Key: true, Text: err.Error()})
		}
		if len(trees) == 0 {
			trees = append(g.trees(), &g.gitTree)
		}
		g.refresh(trees)
		g.h.Redraw()
	})
}

// refresh reads trees again, sooner than their watches would.
func (g *Git) refresh(trees []*gitTree) {
	for _, t := range trees {
		t.mu.Lock()
		t.unread = false
		sub := t.sub
		t.mu.Unlock()
		if sub.Valid() {
			sub.Refresh()
		} else if t.src != nil && t.held {
			t.subscribe()
		}
	}
}

// reading says a subscribed tree, or its line counts, is being read or is about to be.
func (g *Git) reading() bool {
	for _, t := range append(g.trees(), &g.gitTree) {
		t.mu.Lock()
		sub, counts := t.sub, t.counts
		t.mu.Unlock()
		if sub.Valid() && sub.Busy() || counts.Valid() && counts.Busy() {
			return true
		}
	}
	return false
}

func (g *Git) state() *gitx.State {
	if g == nil {
		return nil
	}
	return g.gitTree.State()
}

func (g *Git) load() widget.LoadState {
	if g == nil {
		return widget.LoadState{Phase: widget.Ready}
	}
	return g.gitTree.load()
}

func (g *Git) root() string {
	if g == nil {
		return ""
	}
	return g.gitTree.Root()
}

func (g *Git) rel(p string) string {
	if g == nil {
		return p
	}
	return g.gitTree.Rel(p)
}

func (g *Git) now() time.Time { return kit.WindowNow(g.h) }

// reload reads every work tree's status again, for a Retry.
func (g *Git) reload() { g.async(func(context.Context) error { return nil }) }

// unread starts the work dir's tree over at dir with nothing read, and reads nothing until a reload: a test's.
func (g *Git) unread(dir string) {
	t := &g.gitTree
	t.unsubscribe()
	t.mu.Lock()
	t.unread, t.failed = true, nil
	t.mu.Unlock()
	t.found(dir)
}

// found sets the tree's root; the caller keeps others off it.
func (t *gitTree) found(root string) {
	t.src = t.g.treeSource(root, t.sessionFor(root))
	t.status = gitx.NewStatus(t.src.Repo())
}

// sessionFor is the session whose manager reads a tree at root: the one in front when it's one of its projects,
// else whichever has it, else none (the app's).
func (t *gitTree) sessionFor(root string) core.SessionID {
	st := t.g.h.State()
	holds := func(se core.SessionState) bool {
		for _, p := range se.Projects {
			if p.Dir != "" && (filepath.Clean(p.Dir) == root || kit.Within(root, p.Dir) || kit.Within(p.Dir, root)) {
				return true
			}
		}
		return false
	}
	t.session = ""
	for _, se := range st.Sessions {
		if !se.Archived && holds(se) && (t.session == "" || se.ID == st.Focus) {
			t.session = se.ID
		}
	}
	return t.session
}

// treeSource is root's git sources, from the session's freshness manager, else its own.
func (g *Git) treeSource(root string, id core.SessionID) *gitsrc.Tree {
	if w, ok := services.WatchesOf(g.h); ok {
		return w.Tree(id, root)
	}
	return gitsrc.New(root, nil)
}

// reset makes t the work tree of dir, not yet found, in place: a reading for the tree it was, still on its way, only
// writes the fields under t's locks, which are reset under them.
func (t *gitTree) reset(dir string, asked time.Time) {
	t.syncMu.Lock()
	t.mu.Lock()
	t.session, t.sub, t.remote, t.counts = "", watch.Sub[*gitx.State]{}, watch.Sub[string]{}, watch.Sub[*gitx.State]{}
	t.setAt, t.merged, t.used, t.counted, t.failed = time.Time{}, struct{ st, c, out *gitx.State }{}, false, false, nil
	t.mu.Unlock()
	t.syncMu.Unlock()
	t.status, t.src, t.work, t.ready, t.asked = nil, nil, dir, false, asked
}

// subscribe asks the tree's manager for its status and remote, if it isn't already.
func (t *gitTree) subscribe() {
	if t.src == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.manager()
	if t.sub.Valid() || t.unread || m == nil {
		return
	}
	label := "git: " + t.src.Root
	// Told of each reading from the first on, one already there included, off the loop: status is handed it there.
	status, h := t.status, t.g.h
	var sub watch.Sub[*gitx.State]
	made := make(chan struct{})
	sub = watch.SubscribeFunc(m, t.src.Status(), treeNeed, label, func() {
		<-made
		t.sync(sub, status)
		h.Redraw()
	})
	t.sub = sub
	close(made)
	t.remote = watch.SubscribeFunc(m, t.src.Remote(), treeNeed, label, h.Redraw)
}

func (t *gitTree) unsubscribe() {
	t.mu.Lock()
	sub, remote, counts := t.sub, t.remote, t.counts
	t.sub, t.remote, t.counts = watch.Sub[*gitx.State]{}, watch.Sub[string]{}, watch.Sub[*gitx.State]{}
	t.mu.Unlock()
	if sub.Valid() {
		sub.Close()
		remote.Close()
	}
	if counts.Valid() {
		counts.Close()
	}
}

// sync hands status sub's latest reading, unless it has it, or a later one, already.
func (t *gitTree) sync(sub watch.Sub[*gitx.State], status *gitx.Status) {
	t.syncMu.Lock()
	defer t.syncMu.Unlock()
	st, at, ok := sub.Get()
	t.mu.Lock()
	if !ok {
		if err := sub.Err(); err != nil {
			t.failed = err
		}
		t.mu.Unlock()
		return
	}
	newer := at.After(t.setAt)
	if newer {
		t.setAt, t.failed = at, nil
	}
	t.mu.Unlock()
	if newer {
		status.Set(st)
	}
}

// use keeps the tree subscribed through this frame.
func (t *gitTree) use() {
	if t == nil || t.src == nil {
		return
	}
	t.mu.Lock()
	t.used = true
	subbed := t.sub.Valid()
	t.mu.Unlock()
	if !subbed {
		t.subscribe()
	}
}

// Load is how far the tree's status is read: waiting until its root is found and first read, failed while that read
// fails, and done after; outside a repo it's done with no state.
func (t *gitTree) Load() registry.Load {
	if t == nil {
		return registry.Load{Done: true}
	}
	if !t.ready || t.status != nil && t.State() == nil {
		t.mu.Lock()
		err := t.failed
		t.mu.Unlock()
		return registry.Load{Err: err, Since: t.asked}
	}
	return registry.Load{Done: true}
}

func (t *gitTree) load() widget.LoadState { return kit.LoadState(t.Load()) }

func (t *gitTree) StateWithCounts() *gitx.State { return t.withCounts() }
func (t *gitTree) Status() *gitx.Status {
	if t == nil {
		return nil
	}
	t.use()
	return t.status
}

func (t *gitTree) Remote() string {
	if t == nil {
		return ""
	}
	t.use()
	t.mu.Lock()
	remote := t.remote
	t.mu.Unlock()
	if !remote.Valid() {
		return ""
	}
	url, _, _ := remote.Get()
	return url
}

// gitTrees is git's GitTrees service.
type gitTrees struct{ g *Git }

func (s gitTrees) Tree(dir string) services.GitTree { return s.g.tree(dir) }
func (s gitTrees) Scope() gitx.Scope {
	if s.g.scope == nil {
		return gitx.All
	}
	return s.g.scope()
}
func (s gitTrees) Holding(p string) services.GitTree      { return s.g.holding(p) }
func (s gitTrees) Run(fn func(ctx context.Context) error) { s.g.async(fn, &s.g.gitTree) }
func (s gitTrees) Reload()                                { s.g.reload() }
func (s gitTrees) Busy() bool                             { return s.g.running.Load() > 0 || s.g.reading() }
func (s gitTrees) WatchWindow(fn func(root string, paths []string)) registry.Disposable {
	return registry.DisposeFunc(s.g.watchWindow(fn))
}

// state is the last status read, nil outside a repo or before the first.
func (t *gitTree) State() *gitx.State {
	if t == nil || t.status == nil {
		return nil
	}
	t.use()
	// Each reading reaches status off the loop, as its subscription hears of it (subscribe): handing it over, a diff
	// of every changed file and its watchers told, is too much for a frame with thousands changed.
	return t.status.State()
}

// busy says the tree's status is being read, or is stale and about to be.
func (t *gitTree) busy() bool {
	t.mu.Lock()
	sub := t.sub
	t.mu.Unlock()
	return sub.Valid() && sub.Busy()
}

// withCounts is the status with line counts, kept read while something asks for it each frame; the status
// without them until they're first read.
func (t *gitTree) withCounts() *gitx.State {
	st := t.State()
	if st == nil {
		return nil
	}
	t.mu.Lock()
	t.counted = true
	counts := t.counts
	m := t.manager()
	if !counts.Valid() && m != nil && !t.unread {
		counts = watch.Subscribe(m, t.src.Counted(), treeNeed, "git counts: "+t.src.Root)
		counts.OnChange(t.g.h.Redraw)
		t.counts = counts
	}
	t.mu.Unlock()
	c, _, ok := counts.Get()
	if !ok {
		return st
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.merged.st != st || t.merged.c != c {
		t.merged.st, t.merged.c, t.merged.out = st, c, st.WithCounts(c)
	}
	return t.merged.out
}

// manager is the tree's session's manager, else the app's; t.mu is held.
func (t *gitTree) manager() *watch.Manager {
	w, ok := services.WatchesOf(t.g.h)
	if !ok || t.src == nil {
		return nil
	}
	if m := w.For(t.session); m != nil {
		return m
	}
	t.session = ""
	return w.App()
}

func (t *gitTree) Root() string {
	if t == nil || t.status == nil {
		return ""
	}
	return t.status.Repo().Dir
}

// rel is a path relative to the repo's root, as git names it, from one that's absolute or relative to the work dir.
func (t *gitTree) Rel(p string) string {
	if t.Root() == "" {
		return p
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.work, p)
	}
	if r, err := filepath.Rel(t.Root(), p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

// abs is a repo path as a path on disk.
func (t *gitTree) abs(rel string) string { return filepath.Join(t.Root(), filepath.FromSlash(rel)) }

// settled says every work tree asked for has had its root looked for.
func (g *Git) settled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range g.others {
		if !t.ready {
			return false
		}
	}
	return true
}
