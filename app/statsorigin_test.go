package app

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
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
)

// outside gives the rig's Claude a transcript no Oriel session owns: a week of work in api, run in a terminal.
func (r *statsRig) outside(t *testing.T) string {
	t.Helper()
	fx := fxSession{name: "lint-rules", provider: "claude", model: "claude-sonnet-4-5", repos: []string{"api"}, weights: []int{1}, perDay: 40}
	dirs := map[string]string{"api": filepath.Join(r.repos["api"], "cmd")}
	rnd := rand.New(rand.NewSource(99))
	lines := []string{fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"text","text":"Try the new lint rules"}]}}`,
		statsNow.AddDate(0, 0, -6).Format(time.RFC3339), dirs["api"])}
	for _, d := range []int{0, 2, 4, 6} {
		lines = append(lines, dayLog(rnd, fx, dirs, statsNow.AddDate(0, 0, -d), false)...)
	}
	p := filepath.Join(t.TempDir(), "lint.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.claude.mu.Lock()
	r.claude.found = append(r.claude.found, agent.Transcript{Session: "lint", Logs: []agent.Log{{Path: p, Format: stats.FormatClaude}},
		Modified: statsNow})
	r.claude.mu.Unlock()
	return "claude:lint"
}

// The Stats page shows what ran outside Oriel beside Oriel's own: its share of the tokens, the origin filter, the
// two stacked by day, the session marked in the sessions table, its own stats tab, and Usage's outside row. Everything
// counts in the start view's week.
func TestStatsOriginSplit(t *testing.T) {
	s, r, x := statsScene(t)
	s.settleStats(x)
	id := r.outside(t)
	x.mu.Lock()
	clear(x.reports)
	x.mu.Unlock()
	week := stats.Query{From: statsNow.Truncate(24*time.Hour).AddDate(0, 0, -6), To: statsNow.Truncate(24*time.Hour).AddDate(0, 0, 1),
		Offset: x.offset(), Split: true}
	x.report(week)
	s.settleStats(x)
	all, _ := x.report(week)
	if len(all.Origins) != 2 || all.Origins[1].Name != stats.OriginOutside || all.Origins[1].Sessions != 1 {
		t.Fatalf("origins = %+v", all.Origins)
	}
	if got, want := x.figure("week"), money(all.Totals.Cost)+" this week"; got != want || all.Origins[1].Cost <= 0 {
		t.Errorf("the start view's week = %q, want everything: %q", got, want)
	}

	s.keyCommand(t, "<D-S>")
	s.settleStats(x)
	s.wantFrame(t, "ALL", "ORIEL", "OUTSIDE", "% outside")
	s.click(s.textPoint(t, "OUTSIDE"))
	s.settleStats(x)
	if f := x.q.filters["origin"]; len(f) != 1 || f[0] != stats.OriginOutside {
		t.Fatalf("OUTSIDE set the filter %v", x.q.filters)
	}
	r1, _ := x.report(x.query())
	if r1.Totals.Sessions != 1 || len(r1.Groups) != 1 || r1.Groups[0].Key != "api" {
		t.Errorf("outside only = %d sessions, groups %+v", r1.Totals.Sessions, r1.Groups)
	}
	s.click(s.textPoint(t, "ALL"))
	if _, ok := x.q.filters["origin"]; ok {
		t.Error("ALL kept the origin filter")
	}

	s.keyCommand(t, "go")
	s.settleStats(x)
	if x.q.group != stats.ByOrigin {
		t.Fatalf("g o grouped by %s", x.q.group)
	}
	s.lint("the stats page by origin")
	s.golden("stats-origin")
	s.wantFrame(t, "COST PER ORIGIN PER DAY", "Oriel", "outside Oriel", "ORIGIN")

	s.keyCommand(t, "gs")
	s.settleStats(x)
	s.wantFrame(t, "Try the new lint rules", "outside")
	s.run("stats.open", registry.Args{"session": id})
	s.settleStats(x)
	s.click(centre(s.access("Session stats").Bounds))
	s.settleStats(x)
	if !s.showing(StatsSessionView) {
		t.Fatal("Session stats didn't open the outside session's tab")
	}
	s.wantFrame(t, "Try the new lint rules", "outside Oriel", "COST OVER THE SESSION")
	d, ok := x.detail(core.SessionID(id))
	if !ok || d.Origin != stats.OriginOutside || d.Totals.Requests == 0 {
		t.Errorf("the outside session's stats = %+v", d)
	}
	if err := x.h.Run(context.Background(), "stats.session", registry.Args{"id": "claude:nothing"}); err == nil {
		t.Error("a session nobody has opened a tab")
	}

	// Usage's "burned the window" has them as its outside Oriel row, with what no transcript explains.
	s.keyCommand(t, "<D-U>")
	s.settleStats(x)
	s.wantFrame(t, "BURNED THE WEEKLY WINDOW", "outside Oriel", "1 sess")
}
