package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/internal/leaktest"
	"github.com/liamg/oriel/watch"
)

var quick = watch.Timing{Settle: 30 * time.Millisecond, Most: 300 * time.Millisecond, Grace: time.Second}

type rig struct {
	repo *gittest.Repo
	runs func(...string) int
	m    *watch.Manager
	tree *Tree
}

func newRig(t *testing.T) *rig {
	t.Helper()
	runs := gittest.CountGit(t)
	leaktest.Check(t)
	repo := gittest.New(t)
	repo.Write(map[string]string{"main.go": "package main\n", ".gitignore": "build/\n"})
	repo.Commit("Ada", "first")
	m := watch.New(watch.Options{Name: "test", Timing: quick})
	t.Cleanup(m.Close)
	return &rig{repo: repo, runs: runs, m: m, tree: New(repo.Dir, nil)}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
	}
}

// settle waits out the reads FSEvents brings on as a watch begins: it may report the setup's changes, made just
// before.
func (r *rig) settle(t *testing.T) {
	t.Helper()
	last := r.runs()
	for deadline := time.Now().Add(5 * time.Second); ; {
		time.Sleep(300 * time.Millisecond)
		n := r.runs()
		if n == last {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("git still running %d times in 300ms", n-last)
		}
		last = n
	}
}

func (r *rig) quiet(t *testing.T, what string, d time.Duration) {
	t.Helper()
	before := r.runs()
	time.Sleep(d)
	if n := r.runs() - before; n > 0 {
		t.Fatalf("ran git %d times %s", n, what)
	}
}

// The status put together from the sources is the one read all at once, but for line counts.
func TestStatusMatchesAWholeRead(t *testing.T) {
	r := newRig(t)
	r.repo.Git("checkout", "-q", "-b", "work")
	r.repo.Write(map[string]string{"main.go": "package main\n// x\n", "new.go": "package main\n", "build/out": "x"})
	r.repo.Git("add", "main.go")
	r.repo.Write(map[string]string{"main.go": "package main\n// y\n"})
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	eventually(t, "a status", func() bool { _, _, ok := s.Get(); return ok })
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
	r := newRig(t)
	other := filepath.Join(t.TempDir(), "other")
	r.repo.Git("worktree", "add", "-q", "-b", "other", other)
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	eventually(t, "a status", func() bool { _, _, ok := s.Get(); return ok })
	r.settle(t)
	r.quiet(t, "with nothing changed", time.Second)

	before, refs := r.runs(), r.runs("rev-parse")
	os.WriteFile(filepath.Join(r.repo.Dir, "new.go"), []byte("package main\n"), 0o644)
	eventually(t, "new.go", func() bool { st, _, _ := s.Get(); _, ok := st.File("new.go"); return ok })
	time.Sleep(200 * time.Millisecond)
	if n := r.runs() - before; n != 2 {
		t.Errorf("a file change ran %d gits, want 2 (status, diff)", n)
	}
	if n := r.runs("rev-parse") - refs; n > 0 {
		t.Errorf("a file change read the refs again (%d rev-parses)", n)
	}

	os.MkdirAll(filepath.Join(r.repo.Dir, "build"), 0o755)
	time.Sleep(300 * time.Millisecond) // the new ignored folder is read once
	git := filepath.Join(r.repo.Dir, ".git")
	r.runs()
	before = r.runs()
	for i := range 3 {
		os.WriteFile(filepath.Join(r.repo.Dir, "build", "out"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(git, "worktrees", "other", "index"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(git, "FETCH_HEAD"), []byte{byte(i)}, 0o644)
		os.WriteFile(filepath.Join(other, "main.go"), []byte{byte(i)}, 0o644)
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := r.runs() - before; n > 0 {
		t.Errorf("ran %d gits for an ignored folder, git's housekeeping and another work tree", n)
	}
}

// A commit or a branch change reads the refs and the tracked files again.
func TestRefsFollowCommitsAndBranches(t *testing.T) {
	r := newRig(t)
	s := watch.Subscribe(r.m, r.tree.Status(), watch.Need{}, "test")
	eventually(t, "a status", func() bool { _, _, ok := s.Get(); return ok })
	r.repo.Git("checkout", "-q", "-b", "feature")
	eventually(t, "the branch", func() bool { st, _, _ := s.Get(); return st.Branch == "feature" })
	r.repo.Write(map[string]string{"added.go": "package main\n"})
	r.repo.Commit("Ada", "second")
	eventually(t, "the commit", func() bool {
		st, _, _ := s.Get()
		f, ok := st.File("added.go")
		return ok && f.Area == gitx.Committed
	})
	eventually(t, "added.go tracked", func() bool {
		st, _, _ := s.Get()
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
	eventually(t, "a status", func() bool { _, _, ok := s.Get(); return ok })
	os.WriteFile(filepath.Join(r.repo.Dir, "main.go"), []byte("package main\n// more\n"), 0o644)
	eventually(t, "the change", func() bool { st, _, _ := s.Get(); _, ok := st.File("main.go"); return ok })
	if n := r.runs("--numstat"); n > 0 {
		t.Fatalf("ran %d numstats with no one counting", n)
	}
	c := watch.Subscribe(r.m, r.tree.Counted(), watch.Need{}, "test")
	eventually(t, "the counts", func() bool {
		st, _, _ := c.Get()
		f, ok := st.File("main.go")
		return ok && f.Add == 1
	})
}
