package ship_test

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// A file changed in one project reads that project's git again, and only it: no read of every project follows.
func TestAChangeReadsOnlyItsProject(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	s.Run("ship.open", nil)
	shipSettle(s, t, "both projects read", func() bool {
		_, a := x.ShipOf(se.ID, "search-api")
		_, b := x.ShipOf(se.ID, "web-dashboard")
		return a && b
	})
	shipQuiet(s)
	whole := x.WholeReads(se.ID)
	s.WriteFiles(shipDir(se, "search-api"), map[string]string{"internal/changed.go": "package internal\n"})
	shipSettle(s, t, "search-api read again with the new file", func() bool {
		api, _ := x.ShipOf(se.ID, "search-api")
		return slices.ContainsFunc(api.Files, func(f host.ShipFile) bool { return f.Path == "internal/changed.go" })
	})
	shipQuiet(s)
	if n := x.WholeReads(se.ID) - whole; n > 0 {
		t.Errorf("a change in search-api read every project %d times", n)
	}
}

// shipQuiet runs what's handed to the loop and waits for Ship's reads, until neither starts more: a read's end
// can hand on another (a read asked for while one ran).
func shipQuiet(s *apptest.Scene) {
	for range 3 {
		s.Idle()
		shipIdle(s)
	}
}

// Coming back to a session with a tab reads none of its projects whole: its watches kept it while it was behind
// another.
func TestSwitchingBackReadsNoGit(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	s.Run("ship.open", nil)
	shipSettle(s, t, "both projects read", func() bool {
		_, a := x.ShipOf(se.ID, "search-api")
		_, b := x.ShipOf(se.ID, "web-dashboard")
		return a && b
	})
	shipQuiet(s)
	whole := x.WholeReads(se.ID)
	other := core.SessionState{ID: "other", Name: "other", Kind: core.KindWork}
	s.Apply(core.SessionSynced{Session: other}, core.SessionFocused{ID: other.ID}, core.SessionFocused{ID: se.ID})
	shipQuiet(s) // what the switch starts is handed to the loop first
	if n := x.WholeReads(se.ID) - whole; n > 0 {
		t.Errorf("switching back read every project %d times", n)
	}
}

// Pushing one project reads that project again, and its PR, not every project of the session: a push changes
// nothing of the others.
func TestAPushReadsOnlyItsProject(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	shipQuiet(s)
	whole := x.WholeReads(se.ID)
	s.Run("ship.push", registry.Args{"project": "search-api"})
	shipSettle(s, t, "the push", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Pushed && len(sh.Unpushed) == 0 })
	shipQuiet(s)
	if n := x.WholeReads(se.ID) - whole; n > 0 {
		t.Errorf("pushing search-api read every project %d times", n)
	}
}
