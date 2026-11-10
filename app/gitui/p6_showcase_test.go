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
	s.Until("the diff", func() bool { return strings.Contains(s.Text(), "MARK REVIEWED") })
	s.Run("diff.hunk.next", nil)
	s.Run("diff.review", nil)
	s.Shot("p6-05a-diff-split")
	s.Run("diff.layout", registry.Args{"layout": "unified"})
	s.Shot("p6-05b-diff-unified")
}
