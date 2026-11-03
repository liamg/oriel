package kit

import (
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// TStateRunning is a StateMark's spinner.
var TStateRunning = theme.Declare("agent.tool.running", "status.working")

// What a StateMark shows.
const (
	MarkRunning = "running"
	MarkOK      = "ok"
	MarkFailed  = "failed"
	MarkWaiting = "waiting"
)

// StateMark is a state told by its shape as well as its colour, so it reads without telling colours apart: a spinner
// while running (since when), ✓ done, ✗ failed, ○ waiting.
func StateMark(c *ui.Ctx, key any, state string, since time.Time, size float32) {
	th := c.Theme()
	c.Box(key, ui.Style{W: ui.Px(size + 2), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		switch state {
		case MarkRunning:
			widget.Busy(c, "spin", widget.LoadState{Phase: widget.Pending, Since: since}, widget.BusyOpts{Size: size, Clock: -1, Tone: th.C(TStateRunning)})
		case MarkOK:
			c.Text("g", "✓", UIFont(size+1, th.C(TToolOK)))
		case MarkFailed:
			c.Text("g", "✗", UIFont(size+1, th.C(TToolFailed)))
		default:
			c.Text("g", "○", UIFont(size+1, th.C(theme.FgFaint)))
		}
	})
}

// statusGlyphs are a session status's shapes: red asks for you, so it's "!", not a failure's ✗.
var statusGlyphs = map[core.Status]string{core.StatusNeedsYou: "!", core.StatusPermission: "?", core.StatusCI: "◌", core.StatusReviewer: "…",
	core.StatusQuiet: "✓", core.StatusOff: "○"}

// StatusMark is a session's status told by its shape as well as its colour: a spinner while it works, ! when it needs
// you, ? when it asks permission, ◌ waiting on CI, … on a reviewer, ✓ quiet, ○ off.
func StatusMark(c *ui.Ctx, key any, s core.Status, size float32) {
	th := c.Theme()
	tone := th.C(StatusTokens[s])
	c.Box(key, ui.Style{W: ui.Px(size + 2), Main: ui.Center, Cross: ui.Center, Role: ui.RoleText, Label: string(s)}, func(c *ui.Ctx) {
		switch g, ok := statusGlyphs[s]; {
		case s == core.StatusWorking:
			widget.Busy(c, "spin", widget.LoadState{Phase: widget.Pending}, widget.BusyOpts{Size: size, Clock: -1, Tone: tone})
		case ok:
			c.Text("g", g, UIFont(size+1, tone))
		}
	})
}
