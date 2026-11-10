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

func TestShowcaseDiff(t *testing.T) {
	r := rig.DiffRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.Until("the diff", func() bool { return strings.Contains(s.Text(), "MARK REVIEWED") })
	s.Quiet()
	s.Shot("06-diff-split")
	s.Run("diff.hunk.next", nil)
	s.Run("diff.review", nil)
	s.Run("diff.layout", registry.Args{"layout": "unified"})
	s.Shot("06-diff-unified")
}
