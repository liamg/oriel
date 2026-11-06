package ship_test

import (
	"testing"

	"github.com/liamg/oriel/core"
)

// A file changed in one project reads that project's git again, and only it: the other project's reading stands.
func TestAChangeReadsOnlyItsProject(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	s.Run("ship.open", nil)
	shipSettle(s, t, "both projects read", func() bool {
		_, a := x.ShipOf(se.ID, "search-api")
		_, b := x.ShipOf(se.ID, "web-dashboard")
		return a && b
	})
	web, _ := x.ShipOf(se.ID, "web-dashboard")
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"internal/changed.go": "package internal\n"})
	shipSettle(s, t, "search-api read again with the new file", func() bool {
		api, _ := x.ShipOf(se.ID, "search-api")
		for _, f := range api.Files {
			if f.Path == "internal/changed.go" {
				return true
			}
		}
		return false
	})
	shipIdle(s)
	if now, _ := x.ShipOf(se.ID, "web-dashboard"); !now.GitRead.Equal(web.GitRead) {
		t.Errorf("web-dashboard was read again (%v, then %v) for a change in search-api", web.GitRead, now.GitRead)
	}
}

// Coming back to a session with a tab reads none of its git: its watches kept it while it was behind another.
func TestSwitchingBackReadsNoGit(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	s.Run("ship.open", nil)
	shipSettle(s, t, "both projects read", func() bool {
		_, a := x.ShipOf(se.ID, "search-api")
		_, b := x.ShipOf(se.ID, "web-dashboard")
		return a && b
	})
	shipIdle(s)
	api, _ := x.ShipOf(se.ID, "search-api")
	web, _ := x.ShipOf(se.ID, "web-dashboard")
	other := core.SessionState{ID: "other", Name: "other", Kind: core.KindWork}
	s.Apply(core.SessionSynced{Session: other}, core.SessionFocused{ID: other.ID}, core.SessionFocused{ID: se.ID})
	for range 3 { // what the switch starts is handed to the loop first
		s.RunQueued()
		s.Frame()
		shipIdle(s)
	}
	if a, _ := x.ShipOf(se.ID, "search-api"); !a.GitRead.Equal(api.GitRead) {
		t.Errorf("search-api was read again (%v, then %v)", api.GitRead, a.GitRead)
	}
	if w, _ := x.ShipOf(se.ID, "web-dashboard"); !w.GitRead.Equal(web.GitRead) {
		t.Errorf("web-dashboard was read again (%v, then %v)", web.GitRead, w.GitRead)
	}
}
