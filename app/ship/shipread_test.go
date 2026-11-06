package ship_test

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/host"
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
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"internal/changed.go": "package internal\n"})
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
		s.RunQueued()
		s.Frame()
		shipIdle(s)
	}
}
