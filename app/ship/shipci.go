package ship

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ciRow is a pull request of a session's project with its checks.
type ciRow struct {
	project string
	pr      int
	state   string
	note    string
}

// firstRead says a session's projects are being read for the first time, and since when.
func (x *Ship) firstRead() (time.Time, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, r := range x.reports {
		if r.loading && r.read.IsZero() {
			return r.since, true
		}
	}
	return time.Time{}, false
}

// ciRows are the pull requests the Ship page has read, failing first, then running, then passing.
func (x *Ship) ciRows(now time.Time) []ciRow {
	x.mu.Lock()
	var out []ciRow
	for _, r := range x.reports {
		for _, s := range r.ships {
			if s.PR == nil || s.PR.Merged {
				continue
			}
			row := ciRow{project: s.Project, pr: s.PR.Number, state: ciOf(s.Checks)}
			done := 0
			var started time.Time
			for _, ch := range s.Checks {
				switch ch.State {
				case registry.CheckFailed:
					if row.note == "" {
						row.note = ch.Name + " ✕"
					}
					done++
				case registry.CheckRunning, registry.CheckQueued:
					if !ch.Started.IsZero() && (started.IsZero() || ch.Started.Before(started)) {
						started = ch.Started
					}
				default:
					done++
				}
			}
			switch row.state {
			case registry.CheckRunning:
				row.note = fmt.Sprintf("%d/%d", done, len(s.Checks))
				if !started.IsZero() {
					row.note += " · " + strings.ReplaceAll(kit.Duration(now.Sub(started)), " ", "")
				}
			case registry.CheckOK:
				row.note = "passed"
			case "":
				row.note = "no checks"
			}
			out = append(out, row)
		}
	}
	x.mu.Unlock()
	rank := map[string]int{registry.CheckFailed: 0, registry.CheckRunning: 1, registry.CheckOK: 2}
	slices.SortStableFunc(out, func(a, b ciRow) int {
		if d := rank[a.state] - rank[b.state]; d != 0 {
			return d
		}
		return b.pr - a.pr
	})
	return out
}

func (x *Ship) ciPanel(c *ui.Ctx) {
	th := c.Theme()
	rows := x.ciRows(c.Now())
	if len(rows) == 0 {
		if since, ok := x.firstRead(); ok {
			widget.Busy(c, "none", widget.LoadState{Phase: widget.Pending, Since: since}, widget.BusyOpts{Label: "Reading pull requests", Size: 10.5})
			return
		}
		c.Text("none", "no pull requests read", kit.MonoFont(10.5, th.C(theme.FgGhost)))
		return
	}
	tone := map[string]theme.TokenID{registry.CheckFailed: theme.StatusNeedsYou, registry.CheckRunning: theme.StatusCI, registry.CheckOK: theme.StatusQuiet}
	for i, r := range rows {
		t, ok := tone[r.state]
		if !ok {
			t = theme.StatusOff
		}
		c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText,
			Label: fmt.Sprintf("#%d %s %s", r.pr, r.project, r.note)}, func(c *ui.Ctx) {
			kit.HUDDot(c, "dot", th.C(t))
			c.Text("n", fmt.Sprintf("#%d %s", r.pr, r.project), kit.MonoFont(11, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("w", r.note, kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
	}
}

func (x *Ship) ciNote() string {
	if n := len(x.ciRows(x.Clock())); n > 0 {
		return kit.Counted(n, "PR")
	}
	return ""
}
