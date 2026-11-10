package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/leaktest"
	"github.com/liamg/oriel/watch"
)

var quick = watch.Timing{Settle: 30 * time.Millisecond, Most: 300 * time.Millisecond, Grace: time.Second}

type rig struct {
	repo *gittest.Repo
	runs func(...string) int
	m    *watch.Manager
	tree *Tree

	clock  *clocktest.Fake // set by scripted
	script *fswatch.Script // set by scripted
}

func newRig(t *testing.T) *rig { return makeRig(t, watch.Options{}, &rig{}) }

// scripted is a rig on a clock the test moves, whose file changes are only the ones the test says happened: what
// runs git is exactly what the test does, and nothing git itself writes under .git.
func scripted(t *testing.T) *rig {
	t.Helper()
	r := &rig{clock: clocktest.New(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))}
	return makeRig(t, watch.Options{Clock: r.clock, Watch: func() (*fswatch.Watcher, error) {
		w, s := fswatch.NewScripted()
		r.script = s
		return w, nil
	}}, r)
}

func makeRig(t *testing.T, o watch.Options, r *rig) *rig {
	t.Helper()
	runs := gittest.CountGit(t)
	leaktest.Check(t)
	repo := gittest.New(t)
	repo.Write(map[string]string{"main.go": "package main\n", ".gitignore": "build/\n"})
	repo.Commit("Ada", "first")
	o.Name, o.Timing = "test", quick
	m := watch.New(o)
	t.Cleanup(m.Close)
	r.repo, r.runs, r.m, r.tree = repo, runs, m, New(repo.Dir, nil)
	return r
}

// advance moves a scripted rig's clock on by d, one thing due at a time, letting the reads each starts finish
// before the next.
func (r *rig) advance(d time.Duration) {
	r.m.Idle()
	end := r.clock.Now().Add(d)
	for {
		next, ok := r.clock.Next()
		if !ok || next.After(end) {
			r.clock.Advance(end.Sub(r.clock.Now()))
			r.m.Idle()
			return
		}
		r.clock.Advance(max(0, next.Sub(r.clock.Now())))
		r.m.Idle()
	}
}

// change says the paths under the repo changed, as a scripted rig's file watcher would.
func (r *rig) change(t *testing.T, rel ...string) {
	t.Helper()
	if r.script == nil {
		t.Fatal("nothing is watching files yet")
	}
	for i, p := range rel {
		rel[i] = filepath.Join(r.repo.Dir, p)
	}
	r.script.Change(rel...)
	r.m.Idle()
}

// until waits for a reading of s's that ok accepts, as each new one arrives.
func until[T any](t *testing.T, s watch.Sub[T], what string, ok func(v T) bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		if v, _, has := s.Get(); has && ok(v) {
			return
		}
		select {
		case <-s.Changed():
		case <-deadline:
			t.Fatalf("never: %s", what)
		}
	}
}

func anyReading[T any](T) bool { return true }

// The status put together from the sources is the one read all at once, but for line counts.
func TestStatusMatchesAWholeRead(t *testing.T) {
	r := newRig(t)
	r.repo.Git("checkout", "-q", "-b", "work")
	r.repo.Write(map[string]string{"main.go": "package main\n// x\n", "new.go": "package main\n", "build/out": "x"})
	r.repo.Git("add", "main.go")
	r.repo.Write(map[string]string{"main.go": "package main\n// y\n"})
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	until(t, s, "a status", anyReading)
	got, _, _ := s.Get()
	want, err := gitx.Repo{Dir: r.repo.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for sc := gitx.Changed; sc <= gitx.All; sc++ {
		g, w := got.Files(sc), want.Files(sc)
		if len(g) != len(w) {
			t.Fatalf("%v: %d files, want %d", sc, len(g), len(w))
		}
		for i := range g {
			w[i].Add, w[i].Del, w[i].Binary = 0, 0, false
			if g[i] != w[i] {
				t.Errorf("%v: %+v, want %+v", sc, g[i], w[i])
			}
		}
	}
	if got.Branch != "work" || got.Head != want.Head || !got.Ignored("build/out") {
		t.Errorf("branch %q head %q ignored %v", got.Branch, got.Head, got.Ignored("build/out"))
	}
}

// Nothing changing runs no git; a file changing runs git status and the diff against the base, and nothing else;
// an ignored folder, or git's housekeeping under .git, runs none.
func TestAFileChangeRunsTwoGits(t *testing.T) {
	r := scripted(t)
	other := filepath.Join(t.TempDir(), "other")
	r.repo.Git("worktree", "add", "-q", "-b", "other", other)
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	r.advance(time.Hour)
	if _, _, ok := s.Get(); !ok {
		t.Fatal("no status")
	}
	before := r.runs()
	r.advance(time.Hour)
	if n := r.runs() - before; n > 0 {
		t.Fatalf("ran git %d times with nothing changed", n)
	}

	before, refs := r.runs(), r.runs("rev-parse")
	os.WriteFile(filepath.Join(r.repo.Dir, "new.go"), []byte("package main\n"), 0o644)
	r.change(t, "new.go")
	r.advance(time.Hour)
	if st, _, _ := s.Get(); st == nil {
		t.Fatal("no status")
	} else if _, ok := st.File("new.go"); !ok {
		t.Fatal("new.go isn't in the status")
	}
	if n := r.runs() - before; n != 2 {
		t.Errorf("a file change ran %d gits, want 2 (status, diff)", n)
	}
	if n := r.runs("rev-parse") - refs; n > 0 {
		t.Errorf("a file change read the refs again (%d rev-parses)", n)
	}

	os.MkdirAll(filepath.Join(r.repo.Dir, "build"), 0o755)
	r.change(t, "build") // the new ignored folder is read once
	r.advance(time.Hour)
	git := filepath.Join(r.repo.Dir, ".git")
	before = r.runs()
	for i := range 3 {
		os.WriteFile(filepath.Join(r.repo.Dir, "build", "out"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(git, "worktrees", "other", "index"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(git, "FETCH_HEAD"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(other, "main.go"), []byte{byte(i)}, 0o644)
		r.change(t, filepath.Join("build", "out"), filepath.Join(".git", "worktrees", "other", "index"),
			filepath.Join(".git", "FETCH_HEAD"))
		r.script.Change(filepath.Join(other, "main.go"))
		r.advance(100 * time.Millisecond)
	}
	r.advance(time.Hour)
	if n := r.runs() - before; n > 0 {
		t.Errorf("ran %d gits for an ignored folder, git's housekeeping and another work tree", n)
	}
}

// A commit or a branch change reads the refs and the tracked files again.
func TestRefsFollowCommitsAndBranches(t *testing.T) {
	r := newRig(t)
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	until(t, s, "a status", anyReading)
	r.repo.Git("checkout", "-q", "-b", "feature")
	until(t, s, "the branch", func(st *gitx.State) bool { return st.Branch == "feature" })
	r.repo.Write(map[string]string{"added.go": "package main\n"})
	r.repo.Commit("Ada", "second")
	until(t, s, "the commit", func(st *gitx.State) bool {
		f, ok := st.File("added.go")
		return ok && f.Area == gitx.Committed
	})
	until(t, s, "added.go tracked", func(st *gitx.State) bool {
		for _, f := range st.Files(gitx.All) {
			if f.Path == "added.go" {
				return true
			}
		}
		return false
	})
}

// Line counts are read only while something subscribes to them.
func TestCountsOnlyWhileSubscribed(t *testing.T) {
	r := newRig(t)
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	until(t, s, "a status", anyReading)
	os.WriteFile(filepath.Join(r.repo.Dir, "main.go"), []byte("package main\n// more\n"), 0o644)
	until(t, s, "the change", func(st *gitx.State) bool { _, ok := st.File("main.go"); return ok })
	if n := r.runs("--numstat"); n > 0 {
		t.Fatalf("ran %d numstats with no one counting", n)
	}
	c := watch.Subscribe(r.m, r.tree.Counted(), watch.Need{}, "test")
	until(t, c, "the counts", func(st *gitx.State) bool {
		f, ok := st.File("main.go")
		return ok && f.Add == 1
	})
}
