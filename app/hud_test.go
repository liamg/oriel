package app_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// The HUD drawer held down over the editor: plan usage with its projections, the machine, the
// sessions by status, and today's figures with a plugin's next meeting.
func TestHUDSnapshot(t *testing.T) {
	s, r, _ := apptest.StatsScene(t)
	if err := os.WriteFile(filepath.Join(s.ConfigDir, "init.lua"), []byte(`local L = oriel
L.hud.indicator("next-meeting", { slot = "hud.today" }, L.ui.row{
  L.ui.indicator{ label = "NEXT MEETING · 14:30", value = "Pricing sync" },
  L.ui.text("meet.google.com · 4 people", "faint"),
})
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s.ReloadLua()
	for i, cpu := range []float64{.22, .25, .3, .21, .4, .45, .33, .38, .29, .34} {
		if err := s.Store().Apply(core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: cpu,
			Mem: 11 << 30, MemTotal: 32 << 30, Sampled: rig.StatsNow.Add(time.Duration(i-9) * time.Minute)}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, l := range []struct {
		src, msg string
		lv       int
	}{{"host", "session pricing-rounding: turn started (claude opus, 44k ctx)", 0},
		{"gateway", "allow git fetch origin — policy: repo-default", 0},
		{"egress", "ask proxy.golang.org:443 from sandbox flaky-e2e — awaiting user", 4},
		{"gh", `billing-export: check "e2e (ubuntu)" failed — 3 tests`, 8}} {
		s.Logs.Router.Write(logs.Record{Time: rig.StatsNow.Add(time.Duration(i-10) * time.Second), Level: slog.Level(l.lv), Source: l.src, Msg: l.msg})
	}
	code := apptest.GitHubCode(s)
	t0 := rig.StatsNow.Add(-4 * time.Minute)
	apptest.PluginOf[*ship.Ship](s, "ship").Fixture(r.IDs["pricing-rounding"], false, func([]ship.Read) []ship.Read {
		return []ship.Read{
			{Ship: host.Ship{Project: "pricing"}, Code: code, Repo: "acme/pricing", PR: &registry.PR{Number: 4421, State: "open"},
				Checks: []registry.Check{{Name: "lint", State: registry.CheckOK, Started: t0, Completed: t0.Add(time.Minute)},
					{Name: "e2e", State: registry.CheckRunning, Started: t0}}},
			{Ship: host.Ship{Project: "billing-export"}, Code: code, Repo: "acme/billing-export", PR: &registry.PR{Number: 4419, State: "open"},
				Checks: []registry.Check{{Name: "e2e", State: registry.CheckFailed, Started: t0, Completed: t0.Add(time.Minute)}}},
			{Ship: host.Ship{Project: "dash-filters"}, Code: code, Repo: "acme/dash-filters", PR: &registry.PR{Number: 4417, State: "open"},
				Checks: []registry.Check{{Name: "build", State: registry.CheckOK, Started: t0, Completed: t0.Add(time.Minute)}}},
		}
	})
	s.Hold(render.ModAlt, 300*time.Millisecond)
	s.SettleStats()
	s.Lint("the HUD")
	s.Golden("hud-10")
	s.WantFrame("PLAN USAGE", "2 accounts", "5H", "WK", "38%", "71%", "THIS MACHINE", "mbp", "15m", "SESSIONS", "open", "need you", "TODAY", "since 00:00", "TOKENS", "COST", "CACHE", "THREADS", "REVIEWED", "UNREAD", "NEXT MEETING · 14:30", "Pricing sync", "⌥ RELEASE TO HIDE · CLICK TO PIN", "⌥ HUD", "CPU")
}
