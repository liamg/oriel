package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
)

// gitPoll is how often the work tree's status is read again while nothing else asks.
const gitPoll = 2 * time.Second

// gitExt is the work tree's git status, kept up to date for the explorer, the gutters and the pickers, and the
// git.* commands that change it. Outside a repo it has no status and its commands say so.
type gitExt struct {
	disposer
	sh     *shell
	h      ext.Host
	status *gitx.Status
	cancel context.CancelFunc
	busy   sync.WaitGroup
}

func (g *gitExt) Manifest() ext.Manifest { return ext.Manifest{ID: "git"} }

func (g *gitExt) Activate(ctx context.Context, h ext.Host) error {
	g.h, g.sh.git = h, g
	if dir := g.sh.cfg.WorkDir; dir != "" {
		if root, ok := gitx.Root(ctx, dir); ok {
			g.status = gitx.NewStatus(gitx.Repo{Dir: root})
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
		for i, p := range ps {
			ps[i] = g.rel(p)
		}
		return ps
	}
	inRepo := func(registry.Args) bool { return g.status != nil }
	op := func(fn func(ctx context.Context, r gitx.Repo, ps []string) error) func(registry.Ctx, registry.Args) error {
		return func(_ registry.Ctx, a registry.Args) error {
			if g.status == nil {
				return fmt.Errorf("not in a git repository")
			}
			ps := paths(a)
			if len(ps) == 0 {
				return fmt.Errorf("no path")
			}
			g.async(func(ctx context.Context) error { return fn(ctx, g.status.Repo(), ps) })
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
	if g.status == nil {
		return nil
	}
	g.add(disposeFn(g.status.Watch(func([]string) { h.Redraw() })))
	g.add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "read git status again", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		g.async(func(context.Context) error { return nil })
		return registry.Verdict{}, nil
	}}))
	ctx, g.cancel = context.WithCancel(context.WithoutCancel(ctx))
	g.async(func(context.Context) error { return nil })
	go func() {
		t := time.NewTicker(gitPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.status.Refresh(ctx)
			}
		}
	}()
	return nil
}

func (g *gitExt) Deactivate(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	g.busy.Wait()
	return g.disposer.Deactivate(ctx)
}

// async runs fn off the loop, then reads the status again; an error is a notice.
func (g *gitExt) async(fn func(ctx context.Context) error) {
	g.busy.Add(1)
	go func() {
		defer g.busy.Done()
		ctx := context.Background()
		if err := fn(ctx); err != nil {
			g.h.Notify(core.Notice{ID: "git.error", Severity: core.SevError, Key: true, Text: err.Error()})
		}
		if err := g.status.Refresh(ctx); err != nil {
			g.h.Log().Warn("git status", "err", err)
		}
		g.h.Redraw()
	}()
}

// wait is until the work queued with async is done.
func (g *gitExt) wait() { g.busy.Wait() }

// state is the last status read, nil outside a repo or before the first.
func (g *gitExt) state() *gitx.State {
	if g == nil || g.status == nil {
		return nil
	}
	return g.status.State()
}

func (g *gitExt) root() string {
	if g == nil || g.status == nil {
		return ""
	}
	return g.status.Repo().Dir
}

// rel is a path relative to the repo's root, as git names it, from one that's absolute or relative to the work dir.
func (g *gitExt) rel(p string) string {
	if g.root() == "" {
		return p
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(g.sh.cfg.WorkDir, p)
	}
	if r, err := filepath.Rel(g.root(), p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

// abs is a repo path as a path on disk.
func (g *gitExt) abs(rel string) string { return filepath.Join(g.root(), filepath.FromSlash(rel)) }
