package gitui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
)

// gitPoll is how often a work tree's status is read again when the OS can't watch it.
const gitPoll = 2 * time.Second

// gitSafety is how often every work tree's status is read again while the window has focus, watched or not.
const gitSafety = time.Minute

// Git is the work tree's git status, kept up to date for the explorer, the gutters and the pickers, and the
// git.* commands that change it. Outside a repo it has no status and its commands say so. Other projects' work
// trees (a session's) are read too, from when something first asks for one.
type Git struct {
	kit.Disposer
	gitTree // the work dir's
	h       plugin.Host
	links   kit.Links       // the dirs asked about, resolved
	ctx     context.Context // until it's deactivated
	cancel  context.CancelFunc
	busy    sync.WaitGroup
	running atomic.Int32   // the work busy waits on
	safety  sync.WaitGroup // the safety poll
	cache   *gitx.Cache    // every tree's, closed with the plugin

	mu     sync.Mutex
	others map[string]*gitTree // by the directory asked for

	ask *commitAsk // the commit dialog, while it's open
}

// gitTree is one work tree's status, and its remote; status is nil outside a repo, or until it's found.
type gitTree struct {
	status *gitx.Status
	work   string // the directory paths not absolute are relative to
	remote string
	ready  bool      // its root and remote have been looked for
	asked  time.Time // when it was first asked for

	mu     sync.Mutex
	failed error // why the last read failed, while there's none to show
}

func (g *Git) Manifest() plugin.Manifest { return plugin.Manifest{ID: "git"} }

func (g *Git) Activate(ctx context.Context, h plugin.Host) error {
	g.h = h
	g.others = map[string]*gitTree{}
	g.cache = gitx.NewCache()
	g.Add(h.Contribute(registry.Service{Name: registry.GitTreesService, Impl: gitTrees{g}, Source: "builtin:git"}))
	g.work, g.ready, g.asked = h.Here().Project, true, g.now()
	if dir := g.work; dir != "" {
		if root, ok := gitx.Root(ctx, dir); ok {
			g.status = gitx.NewStatus(gitx.Repo{Dir: root, Cache: g.cache})
			g.remote = g.status.Repo().RemoteURL(ctx)
		}
	}
	for _, c := range g.commands() {
		g.Add(h.Command(c))
	}
	g.activateCommit(h)
	g.ctx, g.cancel = context.WithCancel(context.WithoutCancel(ctx))
	g.safety.Go(func() { kit.PollEvery(g.ctx, h, gitSafety, 0, false, g.readAll) })
	if g.status == nil {
		return nil
	}
	g.Add(kit.OnDispose(g.status.Watch(func([]string) { h.Redraw() })))
	g.Add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "read git status again", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		g.async(func(context.Context) error { return nil })
		return registry.Verdict{}, nil
	}}))
	done := g.start()
	go func() {
		defer done()
		g.gitTree.read(g.ctx)
		g.gitTree.watch(g.ctx, g.h)
		h.Redraw()
	}()
	return nil
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
				ps[i] = t.rel(p)
			}
			g.async(func(ctx context.Context) error { return fn(ctx, t.status.Repo(), ps) })
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
						ps[i] = g.holding(p).rel(p)
					}
					h.Notify(core.Notice{ID: "git.discard", Severity: core.SevQuestion, Title: "Discard changes?",
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

// readAll reads every work tree's status again.
func (g *Git) readAll() {
	ctx := context.Background()
	for _, t := range append(g.trees(), &g.gitTree) {
		if t.status != nil {
			t.read(ctx)
		}
	}
}

// watch reads the status again whenever the tree or its git folder changes, until ctx ends; or every gitPoll when
// the OS can't watch them. The watch leaves out what the status read before it ignores, then reads it again for
// what changed meanwhile.
func (t *gitTree) watch(ctx context.Context, r registry.Registry) {
	if ctx.Err() != nil {
		return
	}
	defer t.read(ctx)
	root := t.root()
	w := kit.WatchPaths(r, gitPoll, func() { t.read(ctx) })
	w.AddTree(root, t.skip)
	if dir, common := gitDirs(root); dir != filepath.Join(root, ".git") {
		w.Add(filepath.Join(dir, "HEAD"))
		w.Add(filepath.Join(dir, "index"))
		w.Add(filepath.Join(common, "packed-refs"))
		w.AddTree(filepath.Join(common, "refs"), nil)
	}
	context.AfterFunc(ctx, w.Close)
}

// skip leaves out of the watch what doesn't change the status: git's objects and logs, node_modules, and what git
// ignores.
func (t *gitTree) skip(rel string) bool {
	switch rel {
	case ".git/objects", ".git/logs", ".git/lfs":
		return true
	}
	if path.Base(rel) == "node_modules" {
		return true
	}
	st := t.state()
	return st.Ignored(rel) || st.Ignored(rel+"/")
}

// gitDirs are a work tree's git folder, and the repository's own that holds its refs: the same but for a linked
// worktree.
func gitDirs(root string) (dir, common string) {
	dir = filepath.Join(root, ".git")
	b, err := os.ReadFile(dir)
	if err != nil {
		return dir, dir
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return dir, dir
	}
	dir = strings.TrimSpace(line)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	common = dir
	if c, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
	}
	return dir, filepath.Clean(common)
}

func (g *Git) trees() []*gitTree {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]*gitTree, 0, len(g.others))
	for _, t := range g.others {
		out = append(out, t)
	}
	return out
}

// tree is dir's work tree: the work dir's when dir is in it, else one read from now on. Until its root is found it
// has no status.
func (g *Git) tree(dir string) *gitTree {
	if dir == "" || g.root() != "" && g.links.Within(dir, g.root()) || filepath.Clean(dir) == filepath.Clean(g.work) {
		return &g.gitTree
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.others[dir]; ok {
		return t
	}
	t := &gitTree{work: dir, asked: g.now()}
	g.others[dir] = t
	done := g.start()
	go func() {
		defer done()
		ctx := context.Background()
		found := &gitTree{work: dir, ready: true, asked: t.asked}
		if root, ok := gitx.Root(ctx, dir); ok {
			found.status = gitx.NewStatus(gitx.Repo{Dir: root, Cache: g.cache})
			found.read(ctx)
			found.remote = found.status.Repo().RemoteURL(ctx)
			found.status.Watch(func([]string) { g.h.Redraw() })
			found.watch(g.ctx, g.h)
		}
		g.mu.Lock()
		g.others[dir] = found
		g.mu.Unlock()
		g.h.Redraw()
	}()
	return t
}

// holding is the work tree a path is in: one already read whose root holds it, else the work dir's.
func (g *Git) holding(p string) *gitTree {
	if filepath.IsAbs(p) {
		for _, t := range g.trees() {
			if t.root() != "" && kit.Within(p, t.root()) {
				return t
			}
		}
	}
	return &g.gitTree
}

// settled says every work tree asked for has been looked at.
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

func (g *Git) Deactivate(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	plugin.Teardown(ctx, func(context.Context) {
		g.safety.Wait()
		g.busy.Wait()
		if g.cache != nil {
			g.cache.Close()
		}
	})
	return g.Disposer.Deactivate(ctx)
}

// async runs fn off the loop, then reads the status again; an error is a notice.
func (g *Git) async(fn func(ctx context.Context) error) {
	done := g.start()
	go func() {
		defer done()
		ctx := context.Background()
		if err := fn(ctx); err != nil {
			g.h.Notify(core.Notice{ID: "git.error", Severity: core.SevError, Key: true, Text: err.Error()})
		}
		for _, t := range append(g.trees(), &g.gitTree) {
			if t.status == nil {
				continue
			}
			if err := t.read(ctx); err != nil {
				g.h.Log().Warn("git status", "err", err)
			}
		}
		g.h.Redraw()
	}()
}

// start counts work off the loop in, for wait and Busy; it answers how to count it out.
func (g *Git) start() func() {
	g.busy.Add(1)
	g.running.Add(1)
	return func() {
		g.running.Add(-1)
		g.busy.Done()
	}
}

// wait is until the work queued with async is done.
func (g *Git) wait() { g.busy.Wait() }

func (g *Git) state() *gitx.State {
	if g == nil {
		return nil
	}
	return g.gitTree.state()
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
	return g.gitTree.root()
}

func (g *Git) rel(p string) string {
	if g == nil {
		return p
	}
	return g.gitTree.rel(p)
}

func (g *Git) now() time.Time { return kit.WindowNow(g.h) }

// reload reads every work tree's status again, for a Retry.
func (g *Git) reload() { g.async(func(context.Context) error { return nil }) }

// read reads the status again, keeping why it failed.
func (t *gitTree) read(ctx context.Context) error {
	err := t.status.Refresh(ctx)
	t.mu.Lock()
	t.failed = err
	t.mu.Unlock()
	return err
}

// Load is how far the tree's status is read: waiting until its root is found and first read, failed while that read
// fails, and done after; outside a repo it's done with no state.
func (t *gitTree) Load() registry.Load {
	if t == nil {
		return registry.Load{Done: true}
	}
	if !t.ready || t.status != nil && t.status.State() == nil {
		t.mu.Lock()
		err := t.failed
		t.mu.Unlock()
		return registry.Load{Err: err, Since: t.asked}
	}
	return registry.Load{Done: true}
}

func (t *gitTree) load() widget.LoadState { return kit.LoadState(t.Load()) }

func (t *gitTree) Root() string        { return t.root() }
func (t *gitTree) State() *gitx.State  { return t.state() }
func (t *gitTree) Rel(p string) string { return t.rel(p) }
func (t *gitTree) Status() *gitx.Status {
	if t == nil {
		return nil
	}
	return t.status
}

func (t *gitTree) Remote() string {
	if t == nil {
		return ""
	}
	return t.remote
}

// gitTrees is git's GitTrees service.
type gitTrees struct{ g *Git }

func (s gitTrees) Tree(dir string) registry.GitTree      { return s.g.tree(dir) }
func (s gitTrees) Holding(p string) registry.GitTree     { return s.g.holding(p) }
func (s gitTrees) Do(fn func(ctx context.Context) error) { s.g.async(fn) }
func (s gitTrees) Reload()                               { s.g.reload() }
func (s gitTrees) Busy() bool                            { return s.g.running.Load() > 0 }

// state is the last status read, nil outside a repo or before the first.
func (t *gitTree) state() *gitx.State {
	if t == nil || t.status == nil {
		return nil
	}
	return t.status.State()
}

func (t *gitTree) root() string {
	if t == nil || t.status == nil {
		return ""
	}
	return t.status.Repo().Dir
}

// rel is a path relative to the repo's root, as git names it, from one that's absolute or relative to the work dir.
func (t *gitTree) rel(p string) string {
	if t.root() == "" {
		return p
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.work, p)
	}
	if r, err := filepath.Rel(t.root(), p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

// abs is a repo path as a path on disk.
func (t *gitTree) abs(rel string) string { return filepath.Join(t.root(), filepath.FromSlash(rel)) }
