package shell_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// blockGit puts a git first on PATH that, for a command line holding what, waits until release before running the
// real git; started waits for one to come.
func blockGit(t *testing.T, what string) (started func(), release func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	mark, gate := filepath.Join(dir, "started"), filepath.Join(dir, "gate")
	script := "#!/bin/sh\ncase \" $* \" in\n*\" " + what + " \"*)\n\ttouch '" + mark + "'\n\twhile [ ! -e '" + gate + "' ]; do sleep 0.02; done\n\t;;\nesac\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	release = func() { os.WriteFile(gate, nil, 0o644) }
	t.Cleanup(release)
	started = func() {
		t.Helper()
		apptest.Poll(t, "git "+what, func() bool { _, err := os.Stat(mark); return err == nil })
	}
	return started, release
}

// slowCommand runs a command on the loop while its git call is held, checks the loop keeps drawing, and while, if
// any, until it's let go, and waits for done to say the command's result has landed.
func slowCommand(t *testing.T, r *apptest.Window, started func(), release func(), id string, args registry.Args, while func(),
	done func(core.State) bool) {
	t.Helper()
	t.Cleanup(release) // before the app stops, which waits for a loop held in git
	if err := r.Do(func() error { return r.Host.Run(context.Background(), id, args) }); err != nil {
		t.Fatal(err)
	}
	started()
	for range 3 {
		before := len(r.Win.Frames())
		r.Do(func() error { r.Host.Redraw(); return nil })
		r.WaitAfter("another frame while "+id+" waits on git", before, func(string) bool { return true })
	}
	if done(r.Store.State()) {
		t.Fatalf("%s landed while git was held", id)
	}
	if while != nil {
		while()
	}
	release()
	rig.WaitState(t, r.Store, id+" landed", done)
}

// A fetch, on the Repos page's f, runs off the loop: the window keeps drawing while git fetches, with a notice up,
// and the fetch lands after.
func TestRepoFetchLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	for _, args := range [][]string{{"clone", "-q", "--bare", lw.Repo, origin}, {"-C", lw.Repo, "remote", "add", "origin", origin}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo, err := lw.W.AddRepoPath(context.Background(), lw.Repo)
	if err != nil {
		t.Fatal(err)
	}
	started, release := blockGit(t, "fetch")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	notice := "repos.fetch." + string(repo.ID)
	fetched := func(core.State) bool {
		return exec.Command("git", "-C", lw.Repo, "rev-parse", "--verify", "-q", "refs/remotes/origin/main").Run() == nil
	}
	slowCommand(t, r, started, release, "repos.fetch", registry.Args{"repo": string(repo.ID)}, func() {
		if !busyNotice(r.Store.State(), notice) {
			t.Errorf("no %s notice while it fetches", notice)
		}
	}, fetched)
	rig.WaitState(t, r.Store, "the fetch's notice gone", func(st core.State) bool { return !busyNotice(st, notice) })
}

// Archiving a session removes its worktrees off the loop: the window keeps drawing while git removes them.
func TestSessionArchiveLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	started, release := blockGit(t, "worktree remove")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	rig.WaitState(t, r.Store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := r.Do(func() error {
		return r.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}})
	}); err != nil {
		t.Fatal(err)
	}
	rig.WaitState(t, r.Store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	archived := func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Archived
	}
	slowCommand(t, r, started, release, "session.archive", registry.Args{"id": "rounding"}, nil, archived)
}

// busyNotice says whether the notice id is up saying what is under way.
func busyNotice(st core.State, id string) bool {
	return slices.ContainsFunc(st.Notifications, func(n core.Notification) bool {
		return n.ID == id && !n.Dismissed && strings.HasSuffix(n.Text, "…")
	})
}

// The same slow work in two windows doesn't wait on each other: what's under way is each window's own, and a window
// closed mid-work holds nothing up in the next.
func TestSlowWorkIsEachWindowsOwn(t *testing.T) {
	held := make(chan struct{})
	defer close(held)
	slow := func(r *apptest.Window) error {
		return r.Do(func() error {
			r.Host.Command(registry.Command{ID: "test.slow", Run: func(ctx registry.Ctx, _ registry.Args) error {
				return plugin.RunSlow(ctx, r.Host, plugin.Slow{ID: "test.slow", Doing: "Waiting"},
					func(context.Context) (struct{}, error) { <-held; return struct{}{}, nil }, nil)
			}})
			return r.Host.Run(context.Background(), "test.slow", nil)
		})
	}
	a := apptest.NewWindow(t, apptest.WindowOpts{Write: map[string]string{"a.txt": "a\n"}, Files: []string{"a.txt"}})
	if err := slow(a); err != nil {
		t.Fatal(err)
	}
	if err := a.Do(func() error { return a.Host.Run(context.Background(), "test.slow", nil) }); !errors.Is(err, plugin.ErrBusy) {
		t.Errorf("the same work again in the same window: %v", err)
	}
	b := apptest.NewWindow(t, apptest.WindowOpts{Write: map[string]string{"b.txt": "b\n"}, Files: []string{"b.txt"}})
	if err := slow(b); err != nil {
		t.Errorf("the same work in another window: %v", err)
	}
}

// Starting a session makes its worktrees off the loop: the window keeps drawing while git adds them.
func TestSessionStartLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	started, release := blockGit(t, "worktree add")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	rig.WaitState(t, r.Store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	slowCommand(t, r, started, release, "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}}, nil,
		func(st core.State) bool { return st.Focus == "rounding" })
}

// Opening a directory starts its direct session off the loop: the window keeps drawing while git finds its repo.
func TestOpenDirectoryLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	started, release := blockGit(t, "--show-toplevel")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	rig.WaitState(t, r.Store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	slowCommand(t, r, started, release, "directory.open", registry.Args{"path": lw.Repo}, nil, func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().InPlace() && se.Snapshot().Dir == lw.Repo
	})
}

// Tagging a repo runs git off the loop: the window keeps drawing while git tags.
func TestRepoTagLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	repo, err := lw.W.AddRepoPath(context.Background(), lw.Repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"user.name", "Ada"}, {"user.email", "ada@example.com"}} {
		if out, err := exec.Command("git", "-C", lw.Repo, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	started, release := blockGit(t, "-a")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	tagged := func(core.State) bool {
		return exec.Command("git", "-C", lw.Repo, "rev-parse", "--verify", "-q", "refs/tags/v0.1.0").Run() == nil
	}
	slowCommand(t, r, started, release, "repos.tag", registry.Args{"repo": string(repo.ID), "name": "v0.1.0", "push": false, "pull": false, "sign": false},
		nil, tagged)
}

// Cleaning up a repo runs git off the loop: the window keeps drawing while git prunes its worktrees, with a notice up.
func TestRepoHousekeepLeavesTheLoopDrawing(t *testing.T) {
	lw := rig.NewHost(t)
	repo, err := lw.W.AddRepoPath(context.Background(), lw.Repo)
	if err != nil {
		t.Fatal(err)
	}
	started, release := blockGit(t, "prune")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	notice := "repos.housekeep." + string(repo.ID)
	var seen bool
	slowCommand(t, r, started, release, "repos.housekeep", registry.Args{"repo": string(repo.ID), "prune": true}, func() {
		seen = busyNotice(r.Store.State(), notice)
		if !seen {
			t.Errorf("no %s notice while it cleans up", notice)
		}
	}, func(st core.State) bool { return seen && !busyNotice(st, notice) })
}

// coverWatch notes whether the opening cover was on screen by the time the window moved, and whether it was stopped.
type coverWatch struct {
	win     *apptest.Window
	covered atomic.Bool
	stopped atomic.Bool
}

func (p *coverWatch) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.coverwatch"} }
func (p *coverWatch) Activate(_ context.Context, h plugin.Host) error {
	kit.OnDirChanged(h, "notes the cover", func(kit.DirChanged) {
		if fs := p.win.Win.Frames(); len(fs) > 0 && strings.Contains(fs[len(fs)-1].Text(), "Opening") {
			p.covered.Store(true)
		}
	})
	return nil
}
func (p *coverWatch) Deactivate(context.Context) error {
	p.stopped.Store(true)
	return nil
}

// Switching what the window is on draws the opening cover at once, then moves onto the new directory underneath
// the plugins, which go on running.
func TestSwitchDrawsTheCoverAndKeepsThePlugins(t *testing.T) {
	p := &coverWatch{}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: t.TempDir(), Plugins: []plugin.Plugin{p}})
	p.win = r
	dir := t.TempDir()
	if err := r.Do(func() error {
		w, _ := kit.WindowOf(r.Host)
		return w.Reopen(dir, "")
	}); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("the window on the new directory", func() bool { return kit.WindowDir(r.Host) == dir })
	if !p.covered.Load() {
		t.Error("the window moved before the opening cover was drawn")
	}
	if p.stopped.Load() {
		t.Error("switching stopped a plugin")
	}
}
