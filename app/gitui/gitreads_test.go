package gitui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// readsOf counts the reads of the git sources of the tree at dir, in session id's manager.
func readsOf(s *apptest.Scene, id core.SessionID, dir string) int {
	n := 0
	for _, st := range kit.ManagerFor(s.Registry(), id).Stats() {
		if strings.HasPrefix(st.Key, "git.") && strings.HasSuffix(st.Key, ":"+dir) {
			n += st.Reads
		}
	}
	return n
}

// gitQuiet frames until git's work is done and no tree is reading, a few times over: a read's end can start another.
func gitQuiet(s *apptest.Scene) {
	for range 3 {
		s.RunQueued()
		s.Frame()
		s.WaitGit()
		time.Sleep(50 * time.Millisecond)
	}
}

// Staging a file in one project reads that project's git again, not the session's others: their trees didn't change.
func TestStagingReadsOnlyItsTree(t *testing.T) {
	s, _, _, se, _ := apptest.ShipScene(t)
	api, web := apptest.ProjectDir(se, "search-api"), apptest.ProjectDir(se, "web-dashboard")
	s.Until("both trees read", func() bool {
		s.Frame()
		return readsOf(s, se.ID, api) > 0 && readsOf(s, se.ID, web) > 0
	})
	gitQuiet(s)
	path := filepath.Join(api, "staged.go")
	if err := os.WriteFile(path, []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Until("the new file seen", func() bool {
		s.Frame()
		st := kit.GitOf(s.Registry()).Tree(api).State()
		if st == nil {
			return false
		}
		_, ok := st.File("staged.go")
		return ok
	})
	gitQuiet(s)
	before := readsOf(s, se.ID, web)
	s.Run("git.stage", registry.Args{"path": path})
	gitQuiet(s)
	if n := readsOf(s, se.ID, web) - before; n > 0 {
		t.Errorf("staging a file in search-api read web-dashboard's git %d times", n)
	}
}

// Coming back to a work tree reads none of its git: its watches kept on while the window was away.
func TestComingBackReadsNoTree(t *testing.T) {
	s, repo := gitScene(t)
	gitQuiet(s)
	before := readsOf(s, "", repo.Dir)
	if before == 0 {
		t.Fatal("the window's tree was never read")
	}
	win, _ := kit.WindowOf(s.Registry())
	for _, to := range []string{t.TempDir(), repo.Dir} {
		if err := win.Reopen(to, ""); err != nil {
			t.Fatal(err)
		}
		s.Reopen()
		gitQuiet(s)
	}
	if n := readsOf(s, "", repo.Dir) - before; n > 0 {
		t.Errorf("coming back read the tree's git %d times", n)
	}
}

// What changed in a work tree while the window was away is there when it comes back: its watches saw it.
func TestComingBackSeesWhatChangedAway(t *testing.T) {
	s, repo := gitScene(t)
	gitQuiet(s)
	win, _ := kit.WindowOf(s.Registry())
	if err := win.Reopen(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	s.Reopen()
	gitQuiet(s)
	repo.Write(map[string]string{"away.go": "package main\n"})
	if err := win.Reopen(repo.Dir, ""); err != nil {
		t.Fatal(err)
	}
	s.Reopen()
	s.Until("the file written while away", func() bool {
		s.Frame()
		st := kit.GitOf(s.Registry()).Tree("").State()
		if st == nil {
			return false
		}
		_, ok := st.File("away.go")
		return ok
	})
}
