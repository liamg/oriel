package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const hudGrab = "⌥ RELEASE TO HIDE · CLICK TO PIN"

func (s *errScene) hud() *hudPlugin { return s.sh.hud }

// hold presses mods (0 lets go) and draws, d later.
func (s *errScene) hold(m render.Mods, d time.Duration) {
	s.ui.Mods(m)
	s.frame()
	s.now = s.now.Add(d)
	s.frame()
	s.frame()
}

func (s *errScene) hudDown() bool {
	return strings.Contains(s.frameText(), "RELEASE TO HIDE") || strings.Contains(s.frameText(), "PINNED")
}

// ui.md 10: "Hold ⌥ to pull the full HUD down over anything … Release to hide, click to pin."
func TestHUDHoldReleasePin(t *testing.T) {
	s := newErrScene(t, "")
	s.now = at(14, 0, 0)
	s.frame()
	s.ui.Mods(render.ModAlt)
	s.frame()
	if s.hudDown() {
		t.Fatal("the HUD came down before ⌥ was held for hud.hold_delay")
	}
	s.now = s.now.Add(300 * time.Millisecond)
	s.frame()
	s.frame()
	if !s.hudDown() {
		t.Fatalf("⌥ held alone didn't bring the HUD down:\n%s", s.frameText())
	}
	s.wantFrame(t, "THIS MACHINE", "SESSIONS", "TODAY", "LOGS", "PLAN USAGE", "CI QUEUE")
	s.lint("the HUD held down")
	s.hold(0, 0)
	if s.hudDown() {
		t.Fatal("letting go of ⌥ didn't put the HUD away")
	}

	// ⌥O is a chord: the HUD stays away.
	s.ui.Mods(render.ModAlt)
	s.ui.Chord()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.frame()
	if s.hudDown() {
		t.Error("a chord with ⌥ brought the HUD down")
	}
	s.hold(0, 0)

	// Held, then clicked: pinned, it stays after ⌥ is let go, and Escape puts it away.
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.click(render.Point{X: 800, Y: 200})
	s.hold(0, 0)
	if !s.hud().pinned || !s.hudDown() {
		t.Fatalf("a click on the held HUD didn't pin it: pinned %v", s.hud().pinned)
	}
	s.wantFrame(t, "PINNED · ESC OR CLICK TO PUT AWAY")
	s.lint("the HUD pinned")
	s.keyCommand(t, "<Esc>")
	s.frame()
	if s.hudDown() {
		t.Error("Escape didn't put the pinned HUD away")
	}

	// The title bar's ⌥ HUD pins it, and its tab puts it away: the visible close.
	s.click(s.textPoint(t, "⌥ HUD"))
	s.frame()
	if !s.hud().pinned {
		t.Fatal("the title bar's ⌥ HUD didn't pin it")
	}
	s.click(s.textPoint(t, "PINNED · ESC OR CLICK TO PUT AWAY"))
	s.frame()
	if s.hud().pinned || s.hudDown() {
		t.Error("a click on the pinned HUD's tab didn't put it away")
	}

	// hud.hold = off: holding does nothing.
	if err := s.reg.Options().Set(HoldOption, registry.LayerUser, registry.At{}, "off"); err != nil {
		t.Fatal(err)
	}
	s.hold(render.ModAlt, time.Second)
	if s.hudDown() {
		t.Error("hud.hold = off, and holding ⌥ still brought the HUD down")
	}
}

// A key or click with no modifier held isn't a chord: the next ⌥ held brings the HUD down.
func TestHUDAfterTyping(t *testing.T) {
	s := newErrScene(t, "")
	s.now = at(14, 0, 0)
	s.frame()
	s.ui.Chord() // a key typed, as the window's loop does
	s.click(render.Point{X: 800, Y: 200})
	s.hold(render.ModAlt, 300*time.Millisecond)
	if !s.hudDown() {
		t.Fatalf("⌥ held after typing didn't bring the HUD down:\n%s", s.frameText())
	}
}

// "Pull the full HUD down over anything": over the settings page, and under an omnibox opened over it.
func TestHUDOverOtherOverlays(t *testing.T) {
	s := newErrScene(t, "")
	s.now = at(14, 0, 0)
	s.run("settings.open", registry.Args{"section": "ui"})
	s.frame()
	s.hold(render.ModAlt, 300*time.Millisecond)
	if !s.hudDown() {
		t.Fatal("the HUD didn't come down over the settings page")
	}
	p := s.textPoint(t, "THIS MACHINE")
	if hit := s.ui.Hit(p); len(hit) == 0 || !strings.Contains(s.ui.Help(hit[0]), "shell/hud") {
		t.Errorf("the settings page is over the HUD at %v", p)
	}
	s.click(p)
	s.hold(0, 0)
	s.run("omnibox.open", nil)
	s.frame()
	s.frame()
	if !s.omni().open || !s.hudDown() {
		t.Fatalf("the omnibox over the pinned HUD: omnibox %v, HUD %v", s.omni().open, s.hudDown())
	}
	s.lint("the omnibox over the pinned HUD")
}

// SH-8: "every panel bound to live events": a store event shows in the panels and the slots with no frame asking
// for it; an indicator is built again only when its events, its every, or a set or refresh say so.
func TestHUDBoundToEvents(t *testing.T) {
	s := luaScene(t, `local L = oriel
builds = 0
L.hud.indicator("loads", { slot = "statusline.right", on = { "MachineChanged" } }, function()
  builds = builds + 1
  return L.ui.indicator{ label = "BUILDS", value = builds }
end)
L.hud.indicator("deploys", { slot = "titlebar.right", every = "30s" }, function()
  builds = builds + 100
  return L.ui.indicator{ label = "DEPLOY", value = builds }
end)
L.hud.indicator("meeting", { slot = "hud.today" }, L.ui.indicator{ label = "NEXT MEETING", value = "none" })
`)
	s.now = at(14, 0, 0)
	for range 5 {
		s.frame()
	}
	s.wantFrame(t, "BUILDS", "1", "DEPLOY", "101")
	machine := func(cpu float64, sampled time.Time) {
		t.Helper()
		if err := s.store.Apply(core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: cpu,
			Mem: 11 << 30, MemTotal: 32 << 30, Sampled: sampled}}); err != nil {
			t.Fatal(err)
		}
		s.frame()
	}
	machine(.34, s.now)
	s.wantFrame(t, "CPU", "34%", "BUILDS", "2")
	machine(.82, s.now.Add(time.Minute))
	s.wantFrame(t, "82%", "3")
	s.now = s.now.Add(31 * time.Second)
	s.frame()
	s.wantFrame(t, "DEPLOY", "203") // every 30s: built again (and the two MachineChanged builds before it)
	s.eval(t, `oriel.hud.set("meeting", oriel.ui.indicator{ label = "NEXT MEETING", value = "Pricing sync 14:30" })`)
	s.eval(t, `oriel.hud.refresh("loads")`)
	s.frame()
	s.wantFrame(t, "BUILDS", "204")
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.wantFrame(t, "NEXT MEETING", "Pricing sync 14:30", "mbp", "12 cores", "CPU", "82%", "MEM", "11.0G", "15m")
	if load := s.machine().loadOf(); len(load) != 2 || load[1] != .82 {
		t.Errorf("the machine's load, from its events: %v", load)
	}
	// A session's status changing shows in the Sessions panel with nothing else asking.
	if err := s.store.Apply(core.SessionOpened{ID: "billing", Name: "billing-export", Kind: core.KindWork, Status: core.StatusQuiet}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	s.wantFrame(t, "billing-export", "0", "need you")
	if err := s.store.Apply(core.SessionStatusChanged{ID: "billing", Status: core.StatusNeedsYou}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	if !strings.Contains(s.fullText(), "1 need you") && !strings.Contains(s.frameText(), "1\nneed you") {
		t.Errorf("the Sessions panel didn't count the session that needs you:\n%s", s.frameText())
	}
	s.sh.cfg.Logs.Router.Write(logs.Record{Time: s.now, Level: 0, Source: "gateway", Msg: "allow git fetch origin"})
	s.frame()
	s.wantFrame(t, "allow git fetch origin")
}

// 10-hud: the drawer held down over the editor, against the mockup: plan usage with its projections, the machine, the
// sessions by status, today's figures with a plugin's next meeting, the log's tail and the CI queue.
func TestHUDSnapshot(t *testing.T) {
	s, r, x := statsScene(t)
	if err := os.WriteFile(filepath.Join(s.sh.cfg.ConfigDir, "init.lua"), []byte(`local L = oriel
L.hud.indicator("next-meeting", { slot = "hud.today" }, L.ui.row{
  L.ui.indicator{ label = "NEXT MEETING · 14:30", value = "Pricing sync" },
  L.ui.text("meet.google.com · 4 people", "faint"),
})
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s.lua().reload(false)
	for i, cpu := range []float64{.22, .25, .3, .21, .4, .45, .33, .38, .29, .34} {
		if err := s.store.Apply(core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: cpu,
			Mem: 11 << 30, MemTotal: 32 << 30, Sampled: statsNow.Add(time.Duration(i-9) * time.Minute)}}); err != nil {
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
		s.sh.cfg.Logs.Router.Write(logs.Record{Time: statsNow.Add(time.Duration(i-10) * time.Second), Level: slog.Level(l.lv), Source: l.src, Msg: l.msg})
	}
	sh := s.ship()
	t0 := statsNow.Add(-4 * time.Minute)
	sh.mu.Lock()
	sh.reports[r.ids["pricing-rounding"]] = &shipReport{ships: []shipRead{
		{Ship: host.Ship{Project: "pricing"}, Code: githubCode(t, sh.h), Repo: "acme/pricing", PR: &registry.PR{Number: 4421, State: "open"},
			Checks: []registry.Check{{Name: "lint", State: registry.CheckOK, Started: t0, Completed: t0.Add(time.Minute)},
				{Name: "e2e", State: registry.CheckRunning, Started: t0}}},
		{Ship: host.Ship{Project: "billing-export"}, Code: githubCode(t, sh.h), Repo: "acme/billing-export", PR: &registry.PR{Number: 4419, State: "open"},
			Checks: []registry.Check{{Name: "e2e", State: registry.CheckFailed, Started: t0, Completed: t0.Add(time.Minute)}}},
		{Ship: host.Ship{Project: "dash-filters"}, Code: githubCode(t, sh.h), Repo: "acme/dash-filters", PR: &registry.PR{Number: 4417, State: "open"},
			Checks: []registry.Check{{Name: "build", State: registry.CheckOK, Started: t0, Completed: t0.Add(time.Minute)}}},
	}}
	sh.mu.Unlock()
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.settleStats(x)
	s.lint("the HUD")
	s.golden("hud-10")
	s.wantFrame(t, "PLAN USAGE", "2 accounts", "5H", "WK", "38%", "71%", "THIS MACHINE", "mbp", "15m", "SESSIONS", "open",
		"need you", "TODAY", "since 00:00", "TOKENS", "COST", "CACHE", "THREADS", "REVIEWED", "UNREAD", "NEXT MEETING · 14:30", "Pricing sync",
		"LOGS", "ALL PROCESSES", "INFO+", "gateway", "allow git fetch origin — policy: repo-default", "CI QUEUE", "3 PRs", "#4419 billing-export",
		"e2e ✕", "#4421 pricing", "1/2 · 4m", "#4417 dash-filters", "passed", hudGrab, "⌥ HUD", "CPU")
}
