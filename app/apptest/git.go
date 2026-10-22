package apptest

import (
	"testing"

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

// ExplorerScene is the app on ExplorerRepo, its status read.
func ExplorerScene(t *testing.T) (*Scene, *gittest.Repo) {
	r := ExplorerRepo(t)
	s := New(t, Options{WorkDir: r.Dir})
	s.WaitGit()
	s.Frame()
	return s, r
}
