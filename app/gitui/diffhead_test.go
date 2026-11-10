package gitui_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// A file's diff from Uncommitted is against the last commit: what isn't committed, in a tab of its own; from Changed,
// the branch's whole change against its base, committed lines too.
// ref: oracle git (git diff HEAD; git diff $(git merge-base HEAD main))
func TestUncommittedsDiffIsAgainstTheLastCommit(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(map[string]string{"f.txt": "one\ntwo, committed\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"})
	r.Commit("Ada", "on the branch")
	path := filepath.Join(r.Dir, "f.txt")
	os.WriteFile(path, []byte("one\ntwo, committed\nthree\nfour\nfive\nsix\nseven\neight\nnine, not yet\nten\n"), 0o644)

	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Run("diff.open", registry.Args{"path": path, "against": "head"})
	s.Until("the uncommitted diff", func() bool { return s.Find("nine, not yet") != nil })
	s.Quiet()
	s.WantFrame("Uncommitted · ")
	if s.Find("two, committed") != nil {
		t.Error("the uncommitted diff shows the branch's committed change, seven lines from what isn't committed")
	}

	s.Run("diff.open", registry.Args{"path": path})
	s.Until("the branch diff, committed and uncommitted lines both", func() bool {
		return s.Find("two, committed") != nil && s.Find("nine, not yet") != nil
	})
}
