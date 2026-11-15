package agentui

import (
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/uitest"
)

// A turn that left background work running hasn't finished the work: the chip still says it's working.
func TestConsoleStatusWithBackgroundWork(t *testing.T) {
	se := core.SessionState{Agent: core.AgentState{Provider: "claude", Running: true, Background: []core.BackgroundTask{{Kind: "shell", Description: "go test ./..."}}}}
	if got, tok := consoleStatus(se); got != "working in the background" || tok != theme.StatusWorking {
		t.Errorf("chip = %q", got)
	}
}

// The working line's dots and clock hold still while the window is behind another app: it asks for no frames.
func TestWorkingLineRestsUnfocused(t *testing.T) {
	u := uitest.New(t, theme.Dark)
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
