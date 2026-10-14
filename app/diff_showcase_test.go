//go:build showcase

package app

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/registry"
)

func TestShowcaseDiff(t *testing.T) {
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	for range 3 {
		s.frame()
		s.diff().wait()
		s.sh.runTodo()
	}
	s.shot(t, "06-diff-split")
	s.run("diff.hunk.next", nil)
	s.run("diff.review", nil)
	s.run("diff.layout", registry.Args{"layout": "unified"})
	s.shot(t, "06-diff-unified")
}
