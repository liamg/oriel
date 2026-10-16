package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
)

// statusFixture is a branch "work" off main that commits a modify, an add, a delete and a rename, then leaves a
// staged add, a staged and an unstaged modify, an untracked file and an ignored directory.
func statusFixture(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{
		".gitignore":                       "vendor/\n",
		"go.mod":                           "module example.com/pricing\n",
		"internal/pricing/round.go":        gittest.Lines(20),
		"internal/pricing/tiers.go":        gittest.Lines(10),
		"internal/pricing/rates.go":        gittest.Lines(5),
		"internal/pricing/legacy_round.go": gittest.Lines(5),
		"internal/billing/amount.go":       "package billing\n\n// Amount is money.\ntype Amount int64\n",
	})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(map[string]string{
		"internal/pricing/round.go":    strings.Replace(gittest.Lines(20), "line 5\n", "five\n", 1) + "line 21\nline 22\n",
		"internal/pricing/tiers.go":    strings.Replace(gittest.Lines(10), "line 1\n", "one\n", 1),
		"internal/pricing/currency.go": "package pricing\n\n// Currency is an ISO 4217 code.\ntype Currency string\n",
	})
	r.Remove("internal/pricing/legacy_round.go")
	r.Git("mv", "internal/billing/amount.go", "internal/billing/money.go")
	r.Commit("Ada", "rounding")
	r.Write(map[string]string{
		"internal/pricing/round_test.go": "package pricing\n\nimport \"testing\"\n",
		"internal/pricing/tiers.go":      strings.Replace(gittest.Lines(10), "line 1\n", "one\n", 1) + "line 11\n",
	})
	r.Git("add", "internal/pricing/round_test.go", "internal/pricing/tiers.go")
	r.Write(map[string]string{
		"internal/pricing/round.go": strings.Replace(strings.Replace(gittest.Lines(20), "line 5\n", "five\n", 1), "line 10\n", "ten\n", 1) +
			"line 21\nline 22\n",
		"scratch.sql":   "select 1;\nselect 2;\n",
		"vendor/dep.go": "package dep\n",
	})
	return r
}

// row is a file state as the tests write it: "M internal/pricing/round.go +4 -2 both".
func row(f FileState) string {
	s := fmt.Sprintf("%c %s", f.Code, f.Path)
	if f.Code == 0 {
		s = "  " + f.Path
	}
	if f.Old != "" {
		s += " <- " + f.Old
	}
	if f.Ignored {
		s += " ignored"
	}
	if f.Add != 0 || f.Del != 0 {
		s += fmt.Sprintf(" +%d -%d", f.Add, f.Del)
	}
	if f.Area != 0 {
		s += " " + f.Area.String()
	}
	return s
}

func rows(fs []FileState) string {
	var out []string
	for _, f := range fs {
		out = append(out, row(f))
	}
	return strings.Join(out, "\n")
}

// FG-1: "fixture repos produce the expected markers". git is the oracle: each marker is what git status and git diff
// say about the fixture.
// ref: oracle git
func TestStatusScopes(t *testing.T) {
	r := statusFixture(t)
	st, err := Repo{Dir: r.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "work" || st.Base != "main" || len(st.BaseHash) < 7 {
		t.Errorf("branch %q base %q at %q", st.Branch, st.Base, st.BaseHash)
	}
	for _, c := range []struct {
		scope Scope
		want  string
	}{
		{Changed, `R internal/billing/money.go <- internal/billing/amount.go committed
A internal/pricing/currency.go +4 -0 committed
D internal/pricing/legacy_round.go +0 -5 committed
M internal/pricing/round.go +4 -2 both
A internal/pricing/round_test.go +3 -0 uncommitted
M internal/pricing/tiers.go +2 -1 both`},
		{Uncommitted, `A internal/pricing/round_test.go +3 -0 staged
M internal/pricing/tiers.go +1 -0 staged
M internal/pricing/round.go +1 -1 unstaged
? scratch.sql +2 -0 untracked`},
		{All, `  .gitignore
  go.mod
R internal/billing/money.go <- internal/billing/amount.go committed
A internal/pricing/currency.go +4 -0 committed
D internal/pricing/legacy_round.go +0 -5 committed
  internal/pricing/rates.go
M internal/pricing/round.go +4 -2 both
A internal/pricing/round_test.go +3 -0 uncommitted
M internal/pricing/tiers.go +2 -1 both
? scratch.sql +2 -0 untracked
  vendor/ ignored`},
	} {
		if got := rows(st.Files(c.scope)); got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.scope, got, c.want)
		}
	}
}

// A conflicted file is U in Uncommitted and All.
// ref: oracle git
func TestStatusConflict(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "one\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "other")
	r.Write(map[string]string{"a.txt": "two\n"})
	r.Commit("Ada", "two")
	r.Git("checkout", "-q", "main")
	r.Write(map[string]string{"a.txt": "three\n"})
	r.Commit("Ada", "three")
	merge := exec.Command("git", "merge", "-q", "other")
	merge.Dir, merge.Env = r.Dir, gittest.Env()
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("the merge didn't conflict:\n%s", out)
	}
	st, err := Repo{Dir: r.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rows(st.Files(Uncommitted)); !strings.Contains(got, "U a.txt") {
		t.Errorf("Uncommitted:\n%s", got)
	}
	if got := rows(st.Files(All)); !strings.Contains(got, "U a.txt") {
		t.Errorf("All:\n%s", got)
	}
}

// A repo with no base branch to compare with has Changed equal to the uncommitted tracked changes, and one with no
// commits has its files as untracked.
// ref: oracle git
func TestStatusWithoutBase(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "one\n"})
	st, err := Repo{Dir: r.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rows(st.Files(Uncommitted)); got != "? a.txt +1 -0 untracked" {
		t.Errorf("no commits, Uncommitted:\n%s", got)
	}
	r.Commit("Ada", "base")
	r.Write(map[string]string{"a.txt": "one\ntwo\n"})
	st, err = Repo{Dir: r.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rows(st.Files(Changed)); got != "M a.txt +1 -0 uncommitted" {
		t.Errorf("on main, Changed:\n%s", got)
	}
}

// The live status tells watchers which paths changed when it refreshes, and only then.
func TestStatusWatch(t *testing.T) {
	r := statusFixture(t)
	s := NewStatus(Repo{Dir: r.Dir})
	got := make(chan []string, 4)
	cancel := s.Watch(func(paths []string) { got <- paths })
	defer cancel()
	ctx := context.Background()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("the first refresh told no one")
	}
	first := s.State()
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-got:
		t.Fatalf("a refresh with nothing changed told watchers %v", p)
	default:
	}
	if s.State() != first {
		t.Error("a refresh with nothing changed made a new reading, so what's cached on the last is thrown away")
	}
	r.Write(map[string]string{"internal/pricing/rates.go": "changed\n"})
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-got:
		if strings.Join(p, " ") != "internal/pricing/rates.go" {
			t.Errorf("changed paths %v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("a change told no one")
	}
	if fs := s.Files(Uncommitted); len(fs) != 5 {
		t.Errorf("after the change, Uncommitted:\n%s", rows(fs))
	}
	if s.State() == first {
		t.Error("a refresh after a change kept the old reading")
	}
}

// Staging, unstaging and discarding whole files move them between Uncommitted's areas as git add, git restore
// --staged and git restore say.
// ref: oracle git
func TestStageUnstageDiscard(t *testing.T) {
	r := statusFixture(t)
	repo, ctx := Repo{Dir: r.Dir}, context.Background()
	unc := func() string {
		st, err := repo.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return rows(st.Files(Uncommitted))
	}
	if err := repo.Stage(ctx, "internal/pricing/round.go", "scratch.sql"); err != nil {
		t.Fatal(err)
	}
	if got, want := unc(), `M internal/pricing/round.go +1 -1 staged
A internal/pricing/round_test.go +3 -0 staged
M internal/pricing/tiers.go +1 -0 staged
A scratch.sql +2 -0 staged`; got != want {
		t.Errorf("after staging:\n%s\nwant:\n%s", got, want)
	}
	if err := repo.Unstage(ctx, "internal/pricing/round.go", "internal/pricing/tiers.go"); err != nil {
		t.Fatal(err)
	}
	if got, want := unc(), `A internal/pricing/round_test.go +3 -0 staged
A scratch.sql +2 -0 staged
M internal/pricing/round.go +1 -1 unstaged
M internal/pricing/tiers.go +1 -0 unstaged`; got != want {
		t.Errorf("after unstaging:\n%s\nwant:\n%s", got, want)
	}
	if err := repo.Discard(ctx, "internal/pricing/tiers.go"); err != nil {
		t.Fatal(err)
	}
	r.Write(map[string]string{"new.txt": "x\n"})
	if err := repo.Discard(ctx, "new.txt"); err != nil {
		t.Fatal(err)
	}
	if got, want := unc(), `A internal/pricing/round_test.go +3 -0 staged
A scratch.sql +2 -0 staged
M internal/pricing/round.go +1 -1 unstaged`; got != want {
		t.Errorf("after discarding tiers.go and an untracked file:\n%s\nwant:\n%s", got, want)
	}
}

// Changed compares with the later merge-base of origin's base branch and the local one of its name: a stale
// origin/main doesn't put the work merged into main since in Changed.
// ref: oracle git
func TestChangedAgainstTheLaterBase(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"old.txt": "x\n"})
	r.Commit("Ada", "base")
	r.Git("update-ref", "refs/remotes/origin/main", "HEAD")
	r.Git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	r.Write(map[string]string{"merged.txt": "x\n"})
	r.Commit("Ada", "merged into main since origin was read")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(map[string]string{"mine.txt": "x\n"})
	r.Commit("Ada", "the branch's own")
	st, err := Repo{Dir: r.Dir}.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range st.Files(Changed) {
		got = append(got, f.Path)
	}
	if strings.Join(got, " ") != "mine.txt" || st.Base != "main" {
		t.Errorf("changed %v against %q, want [mine.txt] against main", got, st.Base)
	}
	r.Git("update-ref", "refs/remotes/origin/main", "main")
	r.Git("checkout", "-q", "main")
	r.Write(map[string]string{"theirs.txt": "x\n"})
	r.Commit("Bob", "on main, not origin")
	r.Git("checkout", "-q", "work")
	r.Git("update-ref", "refs/remotes/origin/main", "main")
	r.Git("update-ref", "refs/heads/main", "main~1")
	st, _ = Repo{Dir: r.Dir}.Status(context.Background())
	if st.Base != "origin/main" {
		t.Errorf("with origin ahead of main, the base is %q", st.Base)
	}
}
