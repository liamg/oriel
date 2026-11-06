package ship_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/render"
)

// A project with nothing to ship (nothing uncommitted, no commits, no PR) is named in a line over the Ship page's
// columns, not given one of its own, until Show; Hide folds it away again.
func TestShipPageFoldsQuietProjects(t *testing.T) {
	s, se := shipSession(t)
	shipOf(s).Fixture(se.ID, false, func(ships []ship.Read) []ship.Read {
		ships[1] = ship.Read{Ship: host.Ship{Project: ships[1].Project, Branch: ships[1].Branch, Base: ships[1].Base, Pushed: true},
			Code: ships[1].Code, Repo: ships[1].Repo, GitRead: s.Now.Add(-time.Minute)}
		return ships
	})
	s.Run("ship.open", nil)
	s.SettleGit()
	columns := func() int { return strings.Count(s.Text(), "\nUncommitted\n") }
	s.WantFrame("1 project with nothing to ship: web-dashboard")
	if n := columns(); n != 1 {
		t.Fatalf("%d columns with web-dashboard quiet, want search-api's alone", n)
	}
	s.ClickOn("Show")
	s.Frame()
	if n := columns(); n != 2 {
		t.Errorf("%d columns after Show", n)
	}
	s.ClickOn("Hide")
	s.Frame()
	if n := columns(); n != 1 {
		t.Errorf("%d columns after Hide", n)
	}
}

// Columns keep their width however many projects there are: the page scrolls sideways past what fits.
func TestShipColumnsScrollSideways(t *testing.T) {
	s, _ := shipSession(t)
	s.Resize(render.Point{X: 900, Y: 800})
	s.Run("ship.open", nil)
	s.SettleGit()
	ax, aw := column(t, s, "search-api")
	bx, bw := column(t, s, "web-dashboard")
	if aw < 380 || bw < 380 || bx < ax+aw {
		t.Errorf("at 900px wide the columns are %v wide at %v and %v wide at %v; want each at least 380, side by side", aw, ax, bw, bx)
	}
}

// column is a project's column on the Ship page as drawn: the first box keyed by its name.
func column(t *testing.T, s *apptest.Scene, name string) (x, w float32) {
	t.Helper()
	prefix := "box " + name + " "
	for _, l := range strings.Split(s.UI().Dump(), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
			var y, h float32
			if _, err := fmt.Sscanf(l[len(prefix):], "%g,%g %gx%g", &x, &y, &w, &h); err == nil {
				return x, w
			}
		}
	}
	t.Fatalf("no column for %s", name)
	return 0, 0
}
