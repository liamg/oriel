package agentui

import (
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const workingHelp = "agent/console#working"

// workingLine says the agent's turn is running: moving dots, who's working, and for how long. It draws nothing
// while the agent is idle.
func workingLine(c *ui.Ctx, key any, se core.SessionState, width ui.Size) {
	a := se.Agent
	if !a.Working {
		return
	}
	th := c.Theme()
	who := kit.ProviderNameIn(c, a.Provider) + " is working"
	c.Row(key, ui.Style{W: width, Gap: 10, Cross: ui.Center, Pad: ui.PadXY(2, 4)}, func(c *ui.Ctx) {
		widget.Dots(c, "dots", widget.DotsOpts{Tone: th.C(theme.StatusWorking), Name: who, Help: workingHelp})
		c.Text("who", who, kit.UIFont(12, th.C(theme.FgMuted)))
		if !a.TurnStarted.IsZero() {
			if c.WindowFocused() && c.WindowVisible() {
				c.WakeAt(c.Now().Truncate(time.Second).Add(time.Second)) // its clock, which holds behind another app
			}
			c.Text("for", "· "+kit.Duration(c.Now().Sub(a.TurnStarted)), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		}
	})
}
