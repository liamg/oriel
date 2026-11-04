package gitx_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// flightFixture is a clone of a bare origin with main pushed, on branch feature cut from it, and a second clone
// ("upstream") that moves main on.
func flightFixture(t *testing.T) (work, upstream *gittest.Repo) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	origin := filepath.Join(t.TempDir(), "origin.git")
	seed := gittest.New(t)
	seed.Git("init", "-q", "--bare", "-b", "main", origin)
	seed.Write(map[string]string{"limiter.go": "package limiter\n\nfunc Allow() bool {\n\treturn true\n}\n", "README": "hi\n"})
	seed.Commit("Seed", "first")
	seed.Git("remote", "add", "origin", origin)
	seed.Git("push", "-q", "origin", "main")
	work = &gittest.Repo{T: t, Dir: filepath.Join(t.TempDir(), "work"), When: seed.When}
	seed.Git("clone", "-q", origin, work.Dir)
	work.Git("checkout", "-q", "-b", "feature")
	upstream = seed
	return work, upstream
}

// A flight checklist per project: uncommitted → behind base → unpushed → PR. Read against a fixture repo.
func TestFlightReadsEachStep(t *testing.T) {
	ctx := context.Background()
	work, upstream := flightFixture(t)
	r := gitx.Repo{Dir: work.Dir}

	f, err := r.Flight(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if f.Branch != "feature" || f.Base != "origin/main" || len(f.Uncommitted) != 0 || f.Behind != 0 || len(f.Commits) != 0 || f.Pushed {
		t.Fatalf("a fresh branch: %+v", f)
	}

	work.Write(map[string]string{"limiter.go": "package limiter\n\nfunc Allow() bool {\n\treturn false\n}\n", "new.go": "package limiter\n\nvar x = 1\n"})
	work.Git("add", "limiter.go")
	work.Write(map[string]string{"limiter.go": "package limiter\n\nfunc Allow() bool {\n\treturn false // now\n}\n\nfunc Take() {}\n"})
	f, _ = r.Flight(ctx, "main")
	var got []string
	for _, fs := range f.Uncommitted {
		got = append(got, fs.Path+" "+string(fs.Code)+" +"+itoa(fs.Add)+" -"+itoa(fs.Del))
	}
	// Staged and unstaged count once, against HEAD, as git diff HEAD --numstat counts them.
	ns := strings.Fields(work.Git("diff", "HEAD", "--numstat", "--", "limiter.go"))
	if want := []string{"limiter.go M +" + ns[0] + " -" + ns[1], "new.go ? +3 -0"}; !slices.Equal(got, want) {
		t.Fatalf("uncommitted = %q, want %q", got, want)
	}

	if err := r.Commit(ctx, "feat: take\n\nTakes one.\nThen another.", "limiter.go"); err != nil {
		t.Fatal(err)
	}
	f, _ = r.Flight(ctx, "main")
	if len(f.Uncommitted) != 1 || f.Uncommitted[0].Path != "new.go" {
		t.Fatalf("committing one path left %+v", f.Uncommitted)
	}
	if len(f.Commits) != 1 || f.Commits[0].Subject != "feat: take" || f.Commits[0].Body != "Takes one.\nThen another." || len(f.Unpushed) != 1 || f.Pushed {
		t.Fatalf("one commit, not pushed: %+v", f)
	}

	if err := r.Push(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(ctx, "test: more"); err != nil {
		t.Fatal(err)
	}
	f, _ = r.Flight(ctx, "main")
	if !f.Pushed || len(f.Commits) != 2 || len(f.Unpushed) != 1 || f.Unpushed[0].Subject != "test: more" || len(f.Uncommitted) != 0 {
		t.Fatalf("pushed one, then another: %+v", f)
	}

	// main moves on: a change that doesn't touch the branch's lines, then one that does.
	upstream.Write(map[string]string{"README": "hello\n"})
	upstream.Commit("Other", "docs")
	upstream.Git("push", "-q", "origin", "main")
	if err := r.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	f, _ = r.Flight(ctx, "main")
	if f.Behind != 1 || len(f.Conflicts) != 0 {
		t.Fatalf("behind by a clean commit: %d, conflicts %q", f.Behind, f.Conflicts)
	}
	upstream.Write(map[string]string{"limiter.go": "package limiter\n\nfunc Allow() bool {\n\treturn !true\n}\n"})
	upstream.Commit("Other", "fix")
	upstream.Git("push", "-q", "origin", "main")
	r.Fetch(ctx)
	f, _ = r.Flight(ctx, "main")
	if f.Behind != 2 || !slices.Equal(f.Conflicts, []string{"limiter.go"}) {
		t.Fatalf("behind by a conflicting commit: %d, conflicts %q", f.Behind, f.Conflicts)
	}

	// The rebase conflicts, says where, and is undone.
	head, _ := r.Head(ctx)
	err = r.Rebase(ctx, f.Base)
	if !errors.Is(err, gitx.ErrConflict) || !strings.Contains(err.Error(), "limiter.go") {
		t.Fatalf("rebase = %v", err)
	}
	if now, _ := r.Head(ctx); now != head || r.Dirty(ctx) {
		t.Fatalf("a conflicting rebase wasn't undone: %s → %s, dirty %v", head, now, r.Dirty(ctx))
	}
}

// Sync: a clean rebase carries uncommitted work over and leaves the branch up to date.
func TestRebaseCarriesWorkOver(t *testing.T) {
	ctx := context.Background()
	work, upstream := flightFixture(t)
	r := gitx.Repo{Dir: work.Dir}
	work.Write(map[string]string{"a.go": "package a\n"})
	work.Commit("You", "feat: a")
	work.Write(map[string]string{"b.go": "package b\n"})
	upstream.Write(map[string]string{"README": "hello\n"})
	upstream.Commit("Other", "docs")
	upstream.Git("push", "-q", "origin", "main")
	r.Fetch(ctx)
	if err := r.Rebase(ctx, "origin/main"); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Flight(ctx, "main")
	if f.Behind != 0 || len(f.Commits) != 1 || len(f.Uncommitted) != 1 || f.Uncommitted[0].Path != "b.go" {
		t.Fatalf("after the rebase: %+v", f)
	}
}

// A rebase replaces what origin's branch has; Push then replaces it too, with a lease: only while origin still has
// what the branch was rebased from, so a push from elsewhere since is never lost.
func TestPushAfterRebase(t *testing.T) {
	ctx := context.Background()
	work, upstream := flightFixture(t)
	r := gitx.Repo{Dir: work.Dir}
	work.Write(map[string]string{"a.go": "package a\n"})
	work.Commit("You", "feat: a")
	if err := r.Push(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	upstream.Write(map[string]string{"README": "hello\n"})
	upstream.Commit("Other", "docs")
	upstream.Git("push", "-q", "origin", "main")
	r.Fetch(ctx)
	if err := r.Rebase(ctx, "origin/main"); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Flight(ctx, "main")
	if f.Replaced != 1 || len(f.Unpushed) != 2 {
		t.Fatalf("after the rebase: replaced %d, unpushed %d; want 1 and 2 (the rebased commit and main's)", f.Replaced, len(f.Unpushed))
	}
	if err := r.Push(ctx, "feature"); err != nil {
		t.Fatalf("pushing the rebased branch: %v", err)
	}
	if f, _ = r.Flight(ctx, "main"); f.Replaced != 0 || len(f.Unpushed) != 0 {
		t.Fatalf("after the push: replaced %d, unpushed %d", f.Replaced, len(f.Unpushed))
	}

	// Someone else pushes to the branch, and a fetch brings it in unseen: the branch, rewritten again, isn't pushed over it.
	upstream.Git("fetch", "-q", "origin")
	upstream.Git("checkout", "-q", "-b", "feature", "origin/feature")
	upstream.Write(map[string]string{"theirs.go": "package theirs\n"})
	upstream.Commit("Other", "feat: theirs")
	upstream.Git("push", "-q", "origin", "feature")
	work.Git("commit", "-q", "--amend", "-m", "feat: a, amended")
	r.Fetch(ctx)
	if err := r.Push(ctx, "feature"); err == nil {
		t.Fatal("pushed over a commit the branch never had")
	}
	if got := strings.TrimSpace(upstream.Git("log", "-1", "--format=%s", "origin/feature")); got != "feat: theirs" {
		t.Fatalf("origin's branch is %q", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// A draft is stale once the code moved on. The fingerprint follows the work tree's code, untracked files
// included, and one taken from the merge base holds still when that work is committed.
func TestFingerprintFollowsTheCode(t *testing.T) {
	ctx := context.Background()
	work, _ := flightFixture(t)
	r := gitx.Repo{Dir: work.Dir}
	fp := func(since string) string {
		t.Helper()
		s, err := r.Fingerprint(ctx, since)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	clean := fp("HEAD")
	work.Write(map[string]string{"limiter.go": "package limiter\n\nfunc Allow() bool {\n\treturn false\n}\n"})
	edited := fp("HEAD")
	if edited == clean || fp("HEAD") != edited {
		t.Fatalf("an edit: %s → %s (and again %s)", clean, edited, fp("HEAD"))
	}
	work.Write(map[string]string{"new.go": "package limiter\n"})
	untracked := fp("HEAD")
	work.Write(map[string]string{"new.go": "package limiter\n\nvar x = 1\n"})
	if untracked == edited || fp("HEAD") == untracked {
		t.Fatal("an untracked file's content doesn't count")
	}
	base := r.MergeBase(ctx, "origin/main")
	if base == "" {
		t.Fatal("no merge base with origin/main")
	}
	before := fp(base)
	if err := r.Commit(ctx, "feat: x"); err != nil {
		t.Fatal(err)
	}
	if after := fp(base); after != before {
		t.Errorf("committing moved the branch's fingerprint: %s → %s", before, after)
	}
	if fp("HEAD") != clean {
		t.Errorf("a clean tree after the commit fingerprints %s, want %s", fp("HEAD"), clean)
	}
}
