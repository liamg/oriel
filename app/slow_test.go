package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// blockGit puts a git first on PATH that, for a command line holding what, waits until release before running the
// real git; started is closed once one has come.
func blockGit(t *testing.T, what string) (started <-chan struct{}, release func()) {
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
	ch := make(chan struct{})
	go func() {
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(mark); err == nil {
				close(ch)
				return
			}
		}
	}()
	return ch, release
}

// slowCommand runs a command on the loop while its git call is held, checks the loop keeps drawing, and while, if
// any, until it's let go, and waits for done to say the command's result has landed.
func slowCommand(t *testing.T, r *running, started <-chan struct{}, release func(), id string, args registry.Args, while func(),
	done func(core.State) bool) {
	t.Helper()
	t.Cleanup(release) // before the app stops, which waits for a loop held in git
	if err := r.do(func() error { return r.host.Run(context.Background(), id, args) }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s never reached git", id)
	}
	for range 3 {
		before := len(r.win.Frames())
		r.do(func() error { r.host.Redraw(); return nil })
		r.waitAfter("another frame while "+id+" waits on git", before, func(string) bool { return true })
	}
	if done(r.store.State()) {
		t.Fatalf("%s landed while git was held", id)
	}
	if while != nil {
		while()
	}
	release()
	waitState(t, r.store, id+" landed", done)
}

// A fetch, on the Repos page's f, runs off the loop: the window keeps drawing while git fetches, with a notice up,
// and the fetch lands after.
func TestRepoFetchLeavesTheLoopDrawing(t *testing.T) {
	lw := newLocalHost(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	for _, args := range [][]string{{"clone", "-q", "--bare", lw.repo, origin}, {"-C", lw.repo, "remote", "add", "origin", origin}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo, err := lw.w.AddRepoPath(context.Background(), lw.repo)
	if err != nil {
		t.Fatal(err)
	}
	started, release := blockGit(t, "fetch")
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	notice := "repos.fetch." + string(repo.ID)
	fetched := func(core.State) bool {
		return exec.Command("git", "-C", lw.repo, "rev-parse", "--verify", "-q", "refs/remotes/origin/main").Run() == nil
	}
	slowCommand(t, r, started, release, "repos.fetch", registry.Args{"repo": string(repo.ID)}, func() {
		if !busyNotice(r.store.State(), notice) {
			t.Errorf("no %s notice while it fetches", notice)
		}
	}, fetched)
	waitState(t, r.store, "the fetch's notice gone", func(st core.State) bool { return !busyNotice(st, notice) })
}

// Archiving a session removes its worktrees off the loop: the window keeps drawing while git removes them.
func TestSessionArchiveLeavesTheLoopDrawing(t *testing.T) {
	lw := newLocalHost(t)
	started, release := blockGit(t, "worktree remove")
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.repo}})
	}); err != nil {
		t.Fatal(err)
	}
	waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
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
