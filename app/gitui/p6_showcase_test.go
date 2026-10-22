//go:build showcase

package gitui_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
)

func TestShowcaseP6Diff(t *testing.T) {
	r := rig.DiffRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Registry().Contribute(registry.Explainer{Name: "show", Explain: rig.ShowExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.WaitGit()
	s.Run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.Until("the diff", func() bool { s.RunQueued(); return strings.Contains(s.Text(), "MARK REVIEWED") })
	s.Run("diff.hunk.next", nil)
	s.Run("diff.review", nil)
	s.Shot("p6-05a-diff-split")
	s.Run("diff.layout", registry.Args{"layout": "unified"})
	s.Shot("p6-05b-diff-unified")
}

func TestShowcaseP6CommitGated(t *testing.T) {
	r := rig.DiffRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.WaitGit()
	s.Registry().Contribute(registry.ReviewLedger{Name: "showcase", Order: -1, Get: func(root string) registry.Ledger {
		f := registry.LedgerFile{Path: "internal/pricing/round.go", All: registry.LedgerCount{Read: 9, Skimmed: 4, Unread: 17},
			Uncommitted: registry.LedgerCount{Skimmed: 4, Unread: 17}}
		return registry.Ledger{Root: root, Ready: true, All: f.All, Uncommitted: f.Uncommitted, Files: []registry.LedgerFile{f}}
	}})
	s.Registry().Options().Set("ledger.gate", registry.LayerUser, registry.At{}, "hard")
	s.Run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.Until("the diff", func() bool { s.RunQueued(); return strings.Contains(s.Text(), "MARK REVIEWED") })
	s.Run("git.commit", registry.Args{"path": r.Dir, "message": "fix(pricing): round half to even", "all": true})
	s.Until("the gate", func() bool { s.RunQueued(); return strings.Contains(s.Text(), "COMMIT GATED") })
	s.Shot("p6-04b-commit-gated")
}
