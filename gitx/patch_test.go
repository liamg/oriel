package gitx

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// The change a reviewer reads is from the merge-base to the work tree as it is, new files included, as git
// diff writes it; a snapshot of the work tree is the next review's start, so a re-review reads only what changed
// since, committed or not. The index isn't touched.
// ref: oracle git
func TestSnapshotDiffs(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() {}\n"})
	r.Commit("Ada", "base")
	base := strings.TrimSpace(r.Git("rev-parse", "--short", "HEAD"))
	r.Git("checkout", "-q", "-b", "feature")
	r.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() int { return 2 }\n"})
	r.Commit("Claude", "round")
	r.Write(map[string]string{"currency.go": "package pricing\n\nvar half = 1\n"})
	ctx := context.Background()
	repo := Repo{Dir: r.Dir}
	if got := repo.MergeBase(ctx, ""); got != base {
		t.Errorf("merge-base %q, want %q", got, base)
	}
	if got := repo.MergeBase(ctx, "main"); got != base {
		t.Errorf("merge-base with main %q, want %q", got, base)
	}
	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := r.Git("status", "--porcelain"); st != "?? currency.go\n" {
		t.Errorf("the snapshot touched the index: %q", st)
	}
	p, err := repo.Diff(ctx, base, snap)
	if err != nil {
		t.Fatal(err)
	}
	r.Git("add", "-N", "currency.go")
	want := r.Git("diff", "--no-color", "-M", base)
	r.Git("reset", "-q", "currency.go")
	if p != want {
		t.Errorf("the change:\n%s\nwant:\n%s", p, want)
	}
	r.Write(map[string]string{"currency.go": "package pricing\n\nvar half = 5\n"})
	next, _ := repo.Snapshot(ctx)
	p, _ = repo.Diff(ctx, snap, next)
	if !strings.Contains(p, "-var half = 1") || !strings.Contains(p, "+var half = 5") || strings.Contains(p, "Round") {
		t.Errorf("since the snapshot:\n%s", p)
	}
	if p, _ := repo.Diff(ctx, next, next); p != "" {
		t.Errorf("no change: %q", p)
	}
}
