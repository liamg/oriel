package gitui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
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

// A file staged and then edited again is one file in Uncommitted's diff, counted as git diff HEAD counts it, not one
// row for what's staged and another for what isn't.
// ref: oracle git (git diff HEAD --numstat)
func TestUncommittedsDiffListsAStagedAndEditedFileOnce(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"f.txt": "one\ntwo, staged\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"})
	r.Git("add", "f.txt")
	r.Write(map[string]string{"f.txt": "one\ntwo, staged\nthree\nfour\nfive\nsix\nseven\neight\nnine, not staged\nten\n"})
	f := strings.Fields(r.Git("diff", "HEAD", "--numstat"))
	path := filepath.Join(r.Dir, "f.txt")

	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Run("diff.open", registry.Args{"path": path, "against": "head"})
	s.Until("the uncommitted diff, staged and unstaged lines both", func() bool {
		return s.Find("two, staged") != nil && s.Find("nine, not staged") != nil
	})
	s.Quiet()
	stats := 0
	var walk func(n *render.AccessNode)
	walk = func(n *render.AccessNode) {
		if n.Name == "+"+f[0]+" -"+f[1] {
			stats++
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(s.UI().AccessTree())
	if stats != 1 {
		t.Errorf("%d file headers counted +%s -%s, want 1:\n%s", stats, f[0], f[1], s.Text())
	}
}
