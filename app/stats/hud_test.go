package stats_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const hudGrab = "⌥ RELEASE TO HIDE · CLICK TO PIN"

func hud(s *apptest.Scene) *stats.HUD { return apptest.PluginOf[*stats.HUD](s, "hud") }

func hudDown(s *apptest.Scene) bool {
	return strings.Contains(s.Text(), "RELEASE TO HIDE") || strings.Contains(s.Text(), "PINNED")
}

// Hold ⌥ to pull the full HUD down over anything; release to hide, click to pin.
func TestHUDHoldReleasePin(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 0, 0)
	s.Frame()
	s.UI().Mods(render.ModAlt)
	s.Frame()
	if hudDown(s) {
		t.Fatal("the HUD came down before ⌥ was held for hud.hold_delay")
	}
	s.Now = s.Now.Add(300 * time.Millisecond)
	s.Frame()
	s.Frame()
	if !hudDown(s) {
		t.Fatalf("⌥ held alone didn't bring the HUD down:\n%s", s.Text())
	}
	s.WantFrame("THIS MACHINE", "SESSIONS", "TODAY", "PLAN USAGE")
	s.Lint("the HUD held down")
	s.Hold(0, 0)
	if hudDown(s) {
		t.Fatal("letting go of ⌥ didn't put the HUD away")
	}

	// ⌥O is a chord: the HUD stays away.
	s.UI().Mods(render.ModAlt)
	s.UI().Chord()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	if hudDown(s) {
		t.Error("a chord with ⌥ brought the HUD down")
	}
	s.Hold(0, 0)

	// Held, then clicked: pinned, it stays after ⌥ is let go, and Escape puts it away.
	s.Hold(render.ModAlt, 300*time.Millisecond)
	s.Click(render.Point{X: 800, Y: 100}, 0)
	s.Hold(0, 0)
	if !hud(s).Pinned() || !hudDown(s) {
		t.Fatalf("a click on the held HUD didn't pin it: pinned %v", hud(s).Pinned())
	}
	s.WantFrame("PINNED · ESC OR CLICK TO PUT AWAY")
	s.Lint("the HUD pinned")
	s.Press("<Esc>")
	s.Frame()
	if hudDown(s) {
		t.Error("Escape didn't put the pinned HUD away")
	}

	// The title bar's ⌥ HUD pins it, and its tab puts it away: the visible close.
	s.Click(s.TextPoint("⌥ HUD"), 0)
	s.Frame()
	if !hud(s).Pinned() {
		t.Fatal("the title bar's ⌥ HUD didn't pin it")
	}
	s.Click(s.TextPoint("PINNED · ESC OR CLICK TO PUT AWAY"), 0)
	s.Frame()
	if hud(s).Pinned() || hudDown(s) {
		t.Error("a click on the pinned HUD's tab didn't put it away")
	}

	// hud.hold = off: holding does nothing.
	if err := s.Registry().Options().Set(stats.HoldOption, registry.LayerUser, registry.At{}, "off"); err != nil {
		t.Fatal(err)
	}
	s.Hold(render.ModAlt, time.Second)
	if hudDown(s) {
		t.Error("hud.hold = off, and holding ⌥ still brought the HUD down")
	}
}

// A key or click with no modifier held isn't a chord: the next ⌥ held brings the HUD down.
func TestHUDAfterTyping(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 0, 0)
	s.Frame()
	s.UI().Chord() // a key typed, as the window's loop does
	s.Click(render.Point{X: 800, Y: 100}, 0)
	s.Hold(render.ModAlt, 300*time.Millisecond)
	if !hudDown(s) {
		t.Fatalf("⌥ held after typing didn't bring the HUD down:\n%s", s.Text())
	}
}

// "Pull the full HUD down over anything": over the settings page, and under an omnibox opened over it.
func TestHUDOverOtherOverlays(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 0, 0)
	s.Run("settings.open", registry.Args{"section": "ui"})
	s.Frame()
	s.Hold(render.ModAlt, 300*time.Millisecond)
	if !hudDown(s) {
		t.Fatal("the HUD didn't come down over the settings page")
	}
	p := s.TextPoint("THIS MACHINE")
	if hit := s.UI().Hit(p); len(hit) == 0 || !strings.Contains(s.UI().Help(hit[0]), "shell/hud") {
		t.Errorf("the settings page is over the HUD at %v", p)
	}
	s.Click(p, 0)
	s.Hold(0, 0)
	s.Run("omnibox.open", nil)
	s.Frame()
	s.Frame()
	if omnibox := s.OmniboxOpen(); !omnibox || !hudDown(s) {
		t.Fatalf("the omnibox over the pinned HUD: omnibox %v, HUD %v", omnibox, hudDown(s))
	}
	s.Lint("the omnibox over the pinned HUD")
}

// Every panel is bound to live events: a store event shows in the panels and the slots with no frame asking
// for it; an indicator is built again only when its events, its every, or a set or refresh say so.
func TestHUDBoundToEvents(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
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
	s.Now = apptest.At(14, 0, 0)
	for range 5 {
		s.Frame()
	}
	s.WantFrame("BUILDS", "1", "DEPLOY", "101")
	machine := func(cpu float64, sampled time.Time) {
		t.Helper()
		if err := s.Store().Apply(core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: cpu,
			Mem: 11 << 30, MemTotal: 32 << 30, Sampled: sampled}}); err != nil {
			t.Fatal(err)
		}
		s.Frame()
	}
	machine(.34, s.Now)
	s.WantFrame("CPU", "34%", "BUILDS", "2")
	machine(.82, s.Now.Add(time.Minute))
	s.WantFrame("82%", "3")
	s.Now = s.Now.Add(31 * time.Second)
	s.Frame()
	s.WantFrame("DEPLOY", "203") // every 30s: built again (and the two MachineChanged builds before it)
	s.Lua(`oriel.hud.set("meeting", oriel.ui.indicator{ label = "NEXT MEETING", value = "Pricing sync 14:30" })`)
	s.Lua(`oriel.hud.refresh("loads")`)
	s.Frame()
	s.WantFrame("BUILDS", "204")
	s.Hold(render.ModAlt, 300*time.Millisecond)
	s.WantFrame("NEXT MEETING", "Pricing sync 14:30", "mbp", "12 cores", "CPU", "82%", "MEM", "11.0 GB", "15m")
	// A session's status changing shows in the Sessions panel with nothing else asking.
	if err := s.Store().Apply(core.SessionOpened{ID: "billing", Name: "billing-export", Kind: core.KindWork, Status: core.StatusQuiet}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	s.WantFrame("billing-export", "0", "need you")
	if err := s.Store().Apply(core.SessionStatusChanged{ID: "billing", Status: core.StatusNeedsYou}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	if !strings.Contains(s.FullText(), "1 need you") && !strings.Contains(s.Text(), "1\nneed you") {
		t.Errorf("the Sessions panel didn't count the session that needs you:\n%s", s.Text())
	}
}
