package stats_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
	hoststats "github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
)

// outside gives the rig's Claude a transcript no Oriel session owns: a week of work in api, run in a terminal.
func outside(t *testing.T, r *rig.Stats) string {
	t.Helper()
	fx := rig.FxSession{Name: "lint-rules", Provider: "claude", Model: "claude-sonnet-4-6", Repos: []string{"api"}, Weights: []int{1}, PerDay: 40}
	dirs := map[string]string{"api": filepath.Join(r.Repos["api"], "cmd")}
	rnd := rand.New(rand.NewSource(99))
	lines := []string{fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"text","text":"Try the new lint rules"}]}}`,
		rig.StatsNow.AddDate(0, 0, -6).Format(time.RFC3339), dirs["api"])}
	for _, d := range []int{0, 2, 4, 6} {
		lines = append(lines, rig.DayLog(rnd, fx, dirs, rig.StatsNow.AddDate(0, 0, -d), false)...)
	}
	p := filepath.Join(t.TempDir(), "lint.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Claude.Found(agent.Transcript{Session: "lint", Logs: []agent.Log{{Path: p, Format: hoststats.FormatClaude}}, Modified: rig.StatsNow})
	return "claude:lint"
}

// The Stats page shows what ran outside Oriel beside Oriel's own: its share of the tokens, the origin filter, the
// two stacked by day, the session marked in the sessions table, its own stats tab, and Usage's outside row. Everything
// counts in the start view's week.
func TestStatsOriginSplit(t *testing.T) {
	s, r, x := statsScene(t)
	settle(s, x)
	id := outside(t, r)
	x.ForgetReports()
	week := hoststats.Query{From: rig.StatsNow.Truncate(24*time.Hour).AddDate(0, 0, -6), To: rig.StatsNow.Truncate(24*time.Hour).AddDate(0, 0, 1),
		Offset: x.Offset(), Split: true}
	x.Report(week)
	settle(s, x)
	all := x.Report(week)
	if len(all.Origins) != 2 || all.Origins[1].Name != hoststats.OriginOutside || all.Origins[1].Sessions != 1 {
		t.Fatalf("origins = %+v", all.Origins)
	}
	if got, want := x.Figure("week"), kit.Money(all.Totals.Cost)+" this week"; got != want || all.Origins[1].Cost <= 0 {
		t.Errorf("the start view's week = %q, want everything: %q", got, want)
	}

	s.Press("<D-S>")
	settle(s, x)
	s.WantFrame("ALL", "ORIEL", "OUTSIDE", "% outside")
	s.Click(s.TextPoint("OUTSIDE"), 0)
	settle(s, x)
	if f := x.Filters()["origin"]; len(f) != 1 || f[0] != hoststats.OriginOutside {
		t.Fatalf("OUTSIDE set the filter %v", x.Filters())
	}
	r1 := x.Report(x.Query())
	if r1.Totals.Sessions != 1 || len(r1.Groups) != 1 || r1.Groups[0].Key != "api" {
		t.Errorf("outside only = %d sessions, groups %+v", r1.Totals.Sessions, r1.Groups)
	}
	s.Click(s.TextPoint("ALL"), 0)
	if _, ok := x.Filters()["origin"]; ok {
		t.Error("ALL kept the origin filter")
	}

	s.Press("go")
	settle(s, x)
	if x.Group() != hoststats.ByOrigin {
		t.Fatalf("g o grouped by %s", x.Group())
	}
	s.Lint("the stats page by origin")
	s.Golden("stats-origin")
	s.WantFrame("COST PER ORIGIN PER DAY", "Oriel", "outside Oriel", "ORIGIN")

	s.Press("gs")
	settle(s, x)
	s.WantFrame("Try the new lint rules", "outside")
	s.Run("stats.open", registry.Args{"session": id})
	settle(s, x)
	s.Click(apptest.Centre(s.Node("Session stats").Bounds), 0)
	settle(s, x)
	if !s.Showing(stats.StatsSessionView) {
		t.Fatal("Session stats didn't open the outside session's tab")
	}
	s.WantFrame("Try the new lint rules", "outside Oriel", "COST OVER THE SESSION")
	d, ok := x.Session(core.SessionID(id))
	if !ok || d.Origin != hoststats.OriginOutside || d.Totals.Requests == 0 {
		t.Errorf("the outside session's stats = %+v", d)
	}
	if err := s.Host().Run(context.Background(), "stats.session", registry.Args{"id": "claude:nothing"}); err == nil {
		t.Error("a session nobody has opened a tab")
	}

	// Usage's "burned the window" has them as its outside Oriel row, with what no transcript explains.
	s.Press("<D-U>")
	settle(s, x)
	s.WantFrame("BURNED THE WEEKLY WINDOW", "outside Oriel", "1 sess")
}
