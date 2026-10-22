package agentui

import (
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// startingCard says an agent is starting: a spinner, what its start is doing and for how long, and why it's slow
// when that's worth saying. It draws nothing once the agent is up.
func startingCard(c *ui.Ctx, key any, se core.SessionState, width float32) {
	s := se.Agent.Starting
	if s == nil {
		return
	}
	th := c.Theme()
	c.WakeAt(c.Now().Truncate(time.Second).Add(time.Second)) // its clock
	c.Column(key, ui.Style{W: ui.Px(width), Pad: ui.PadXY(14, 12), Gap: 6, Fill: th.C(tBlockBg), Stroke: th.C(tBlockLine), Border: 1, Radius: 6,
		Role: ui.RoleStatus, Label: s.Step, Help: kit.StartingHelp}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Spinner(c, "spin", widget.SpinnerOpts{Tone: th.C(theme.StatusWorking), Name: "starting " + kit.ProviderNameIn(c, se.Agent.Provider),
				Help: kit.StartingHelp})
			c.Text("step", s.Step+"…", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("for", kit.Duration(c.Now().Sub(s.Since)), kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		if s.Note != "" {
			widget.Wrap(c, "note", s.Note, width-28, kit.UIFont(12.5, th.C(theme.FgMuted)))
		}
		c.Text("then", kit.ProviderNameIn(c, se.Agent.Provider)+" gets your request once it's up.", kit.UIFont(12, th.C(theme.FgFaint)))
	})
}
