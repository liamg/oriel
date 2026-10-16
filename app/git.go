package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// gitPoll is how often the work tree's status is read again while nothing else asks.
const gitPoll = 2 * time.Second

// gitPlugin is the work tree's git status, kept up to date for the explorer, the gutters and the pickers, and the
// git.* commands that change it. Outside a repo it has no status and its commands say so. Other projects' work
// trees (a session's, ui.md 01h) are read too, from when something first asks for one.
type gitPlugin struct {
	disposer
	gitTree // the work dir's
	sh      *shell
	h       plugin.Host
	cancel  context.CancelFunc
	busy    sync.WaitGroup

	mu     sync.Mutex
	others map[string]*gitTree // by the directory asked for

	ask *commitAsk // the commit dialog, while it's open
}

// gitTree is one work tree's status, and its remote; status is nil outside a repo, or until it's found.
type gitTree struct {
	status *gitx.Status
	work   string // the directory paths not absolute are relative to
	remote string
	ready  bool // its root and remote have been looked for
}

func (g *gitPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "git"} }

func (g *gitPlugin) Activate(ctx context.Context, h plugin.Host) error {
	g.h, g.sh.git = h, g
	g.others = map[string]*gitTree{}
	g.work, g.ready = g.sh.cfg.WorkDir, true
	if dir := g.sh.cfg.WorkDir; dir != "" {
		if root, ok := gitx.Root(ctx, dir); ok {
			g.status = gitx.NewStatus(gitx.Repo{Dir: root})
			g.remote = g.status.Repo().RemoteURL(ctx)
		}
	}
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
	for _, c := range []registry.Command{
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
	} {
		g.add(h.Command(c))
	}
	g.activateCommit(h)
	if g.status == nil {
		ctx, g.cancel = context.WithCancel(context.WithoutCancel(ctx))
		go g.poll(ctx)
		return nil
	}
	g.add(disposeFn(g.status.Watch(func([]string) { h.Redraw() })))
	g.add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "read git status again", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		g.async(func(context.Context) error { return nil })
		return registry.Verdict{}, nil
	}}))
	ctx, g.cancel = context.WithCancel(context.WithoutCancel(ctx))
	g.async(func(context.Context) error { return nil })
	go g.poll(ctx)
	return nil
}

// poll reads every work tree's status again every gitPoll, until ctx ends.
func (g *gitPlugin) poll(ctx context.Context) {
	t := time.NewTicker(gitPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if g.status != nil {
				g.status.Refresh(ctx)
			}
			for _, o := range g.trees() {
				if o.status != nil {
					o.status.Refresh(ctx)
				}
			}
		}
	}
}

func (g *gitPlugin) trees() []*gitTree {
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
func (g *gitPlugin) tree(dir string) *gitTree {
	if dir == "" || g.root() != "" && within(dir, g.root()) || filepath.Clean(dir) == filepath.Clean(g.work) {
		return &g.gitTree
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.others[dir]; ok {
		return t
	}
	t := &gitTree{work: dir}
	g.others[dir] = t
	g.busy.Add(1)
	go func() {
		defer g.busy.Done()
		ctx := context.Background()
		found := &gitTree{work: dir, ready: true}
		if root, ok := gitx.Root(ctx, dir); ok {
			found.status = gitx.NewStatus(gitx.Repo{Dir: root})
			found.status.Refresh(ctx)
			found.remote = found.status.Repo().RemoteURL(ctx)
			found.status.Watch(func([]string) { g.h.Redraw() })
		}
		g.mu.Lock()
		g.others[dir] = found
		g.mu.Unlock()
		g.h.Redraw()
	}()
	return t
}

// holding is the work tree a path is in: one already read whose root holds it, else the work dir's.
func (g *gitPlugin) holding(p string) *gitTree {
	if filepath.IsAbs(p) {
		for _, t := range g.trees() {
			if t.root() != "" && within(p, t.root()) {
				return t
			}
		}
	}
	return &g.gitTree
}

// settled says every work tree asked for has been looked at.
func (g *gitPlugin) settled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range g.others {
		if !t.ready {
			return false
		}
	}
	return true
}

func (g *gitPlugin) Deactivate(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	g.busy.Wait()
	return g.disposer.Deactivate(ctx)
}

// async runs fn off the loop, then reads the status again; an error is a notice.
func (g *gitPlugin) async(fn func(ctx context.Context) error) {
	g.busy.Add(1)
	go func() {
		defer g.busy.Done()
		ctx := context.Background()
		if err := fn(ctx); err != nil {
			g.h.Notify(core.Notice{ID: "git.error", Severity: core.SevError, Key: true, Text: err.Error()})
		}
		for _, t := range append(g.trees(), &g.gitTree) {
			if t.status == nil {
				continue
			}
			if err := t.status.Refresh(ctx); err != nil {
				g.h.Log().Warn("git status", "err", err)
			}
		}
		g.h.Redraw()
	}()
}

// wait is until the work queued with async is done.
func (g *gitPlugin) wait() { g.busy.Wait() }

func (g *gitPlugin) state() *gitx.State {
	if g == nil {
		return nil
	}
	return g.gitTree.state()
}

func (g *gitPlugin) root() string {
	if g == nil {
		return ""
	}
	return g.gitTree.root()
}

func (g *gitPlugin) rel(p string) string {
	if g == nil {
		return p
	}
	return g.gitTree.rel(p)
}

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
