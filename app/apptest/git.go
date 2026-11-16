package apptest

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/internal/clock/clocktest"

	"github.com/liamg/oriel/gitx/gittest"
)

// ExplorerRepo is a branch off main that commits a change, an add, a delete and a rename, then stages one file,
// leaves another modified and one untracked, with vendor/ ignored.
func ExplorerRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{
		".gitignore":                       "vendor/\n",
		"go.mod":                           "module example.com/pricing\n",
		"internal/pricing/round.go":        "package pricing\n\nfunc Round() {}\n",
		"internal/pricing/legacy_round.go": "package pricing\n\n// Legacy rounding.\nfunc Legacy() int { return 1 }\n",
		"internal/billing/amount.go":       "package billing\n\n// Amount is money.\ntype Amount int64\n",
	})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "rethink/rounding")
	r.Write(map[string]string{
		"internal/pricing/round.go":    "package pricing\n\nfunc Round() {}\n\nfunc RoundHalfEven() {}\n",
		"internal/pricing/currency.go": "package pricing\n\ntype Currency string\n",
	})
	r.Remove("internal/pricing/legacy_round.go")
	r.Git("mv", "internal/billing/amount.go", "internal/billing/money.go")
	r.Commit("Ada", "rounding")
	r.Write(map[string]string{
		"internal/pricing/round_test.go": "package pricing\n\nimport \"testing\"\n",
		"internal/pricing/round.go":      "package pricing\n\nfunc Round() {}\n\nfunc RoundHalfEven() {}\n\nfunc RoundMinor() {}\n",
		"scratch.sql":                    "select 1;\n",
		"vendor/dep/dep.go":              "package dep\n",
	})
	r.Git("add", "internal/pricing/round_test.go")
	return r
}

// ExplorerScene is the app on ExplorerRepo, its status read, its inline blame's clock two weeks after its commits.
func ExplorerScene(t *testing.T) (*Scene, *gittest.Repo) {
	r := ExplorerRepo(t)
	s := New(t, Options{WorkDir: r.Dir})
	PluginOf[*editor.GitSigns](s, "gitsigns").Clock = clocktest.New(r.When.Add(14 * 24 * time.Hour))
	s.WaitGit()
	s.Frame()
	return s, r
}

// WaitGit waits for the git work running off the loop, as the GitTrees service says, running what it hands the loop.
func (s *Scene) WaitGit() {
	for kit.Git(s.Registry()).Busy() {
		s.RunQueued()
		time.Sleep(time.Millisecond)
	}
}

// SettleGit frames until every project's git status has come: the window's work tree's first read, and each project's,
// as the GitTrees service says.
func (s *Scene) SettleGit() {
	s.T.Helper()
	deadline := time.Now().Add(Timeout)
	for {
		s.Frame()
		s.RunQueued()
		if !kit.Git(s.Registry()).Busy() && s.gitRead() {
			s.Frame()
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatal("the projects' git status never came")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gitRead says the window's work tree and each project's have been read once.
func (s *Scene) gitRead() bool {
	trees := kit.Git(s.Registry())
	dirs := []string{""}
	for _, p := range kit.ProjectsHere(s.Host()).Projects {
		dirs = append(dirs, p.Dir)
	}
	for _, d := range dirs {
		if !trees.Tree(d).Load().Done {
			return false
		}
	}
	return true
}
