package agentui

import (
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Ori blinks unevenly, now and then twice, each blink as long as OriBlinkFor, and says when it next changes.
func TestOriBlinks(t *testing.T) {
	cycle := time.Unix(0, 0).Add(10 * oriBlinkCycle)
	for _, c := range []struct {
		at     time.Duration
		shut   bool
		change time.Duration
	}{
		{0, true, OriBlinkFor},
		{OriBlinkFor, false, 5200*time.Millisecond - OriBlinkFor},
		{5300 * time.Millisecond, true, 40 * time.Millisecond},
		{5400 * time.Millisecond, false, 150 * time.Millisecond},
		{5600 * time.Millisecond, true, 90 * time.Millisecond},
		{12 * time.Second, false, 4 * time.Second},
	} {
		shut, next := OriBlink(cycle.Add(c.at))
		if shut != c.shut || next.Sub(cycle.Add(c.at)) != c.change {
			t.Errorf("at %v: shut %v, changes in %v; want %v, %v", c.at, shut, next.Sub(cycle.Add(c.at)), c.shut, c.change)
		}
	}
}

// Ori is decorative: working, it glances at most 15 times a second, and with the window behind another app or
// hidden it holds still, asking for no frames.
func TestOriRestsUnfocused(t *testing.T) {
	u := testUI(t)
	clk := clocktest.New(time.Unix(1000, 0))
	u.SetClock(clk)
	for _, busy := range []bool{true, false} {
		frame := func() {
			u.Layout(render.Point{X: 40, Y: 40}, func(c *ui.Ctx) { ori(c, "ori", 24, oriMood{busy: busy}) })
		}
		u.SetWindow(true, true)
		frame()
		next := u.NextFrame()
		if next.IsZero() {
			t.Fatalf("busy %v: focused, Ori asked for no frame", busy)
		}
		if busy {
			clk.Set(next)
			frame()
			if step := u.NextFrame().Sub(next); step < ui.DecorInterval {
				t.Errorf("busy: frames %v apart, want at most 15 a second", step)
			}
		}
		for _, w := range [][2]bool{{false, true}, {true, false}} {
			u.SetWindow(w[0], w[1])
			frame()
			frame()
			if next := u.NextFrame(); !next.IsZero() {
				t.Errorf("busy %v, focused %v visible %v: a frame was asked for at %v", busy, w[0], w[1], next)
			}
		}
	}
}

func testUI(t *testing.T) *ui.UI {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	u := ui.New(ts, ui.TextStyle{})
	u.SetTheme(th)
	o := u.Options()
	o.ReducedMotion = false
	u.SetOptions(o)
	return u
}

// The working line's dots and clock hold still while the window is behind another app: it asks for no frames.
func TestWorkingLineRestsUnfocused(t *testing.T) {
	u := testUI(t)
	now := time.Unix(1000, 0)
	u.SetClock(clocktest.New(now))
	se := core.SessionState{Agent: core.AgentState{Working: true, TurnStarted: now.Add(-time.Minute)}}
	frame := func() { u.Layout(render.Point{X: 400, Y: 40}, func(c *ui.Ctx) { workingLine(c, "w", se, ui.Flex(1)) }) }
	frame()
	if u.NextFrame().IsZero() {
		t.Fatal("focused, the working line asked for no frame")
	}
	u.SetWindow(false, true)
	frame()
	frame()
	if next := u.NextFrame(); !next.IsZero() {
		t.Errorf("unfocused, a frame was asked for at %v", next)
	}
}
