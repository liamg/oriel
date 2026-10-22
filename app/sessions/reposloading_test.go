package sessions_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// While loading, the Add repo dialog is Gate. Until the path is checked, Add waits and ⌘↵ says why; once it's
// checked, Add adds it.
func TestAddRepoWaitsForItsCheck(t *testing.T) {
	lw := rig.NewHost(t)
	other := rig.NewRepo(t, filepath.Join(t.TempDir(), "billing"))
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = time.Date(2026, 10, 13, 14, 30, 0, 0, time.UTC)
	s.Run("repos.new", registry.Args{"path": other})
	s.Frame()
	s.Run("repos.new.submit", nil)
	s.Frame()
	s.WantFrame("To add: a moment: checking the path.")
	if _, ok := s.Store().State().Repo("billing"); ok {
		t.Fatal("added before the path was checked")
	}
	s.Now = s.Now.Add(time.Second)
	s.Until("the check", func() bool { return strings.Contains(s.Text(), "✓ git repo") })
	s.Run("repos.new.submit", nil)
	rig.WaitState(t, s.Store(), "billing registered", func(st core.State) bool { _, ok := st.Repo("billing"); return ok })
}

// A clone shows git's progress as a bar: the phase and how far through it.
func TestCloneShowsItsProgress(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/liamg/oriel"})
	reposOf(s).Cloning("Receiving objects:  45% (450/1000), 1.20 MiB | 2.00 MiB/s")
	s.Frame()
	if n := s.Node("Receiving objects 45%"); n.Role != "status" {
		t.Errorf("the bar reads as %+v", n)
	}
	s.WantFrame("Cloning…")
}
