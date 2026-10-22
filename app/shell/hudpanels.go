package shell

import (
	"context"
	"fmt"
	"slices"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The HUD's panels, each contributed by the plugin that owns its data, as indicators in the hud slot.
// They read the store as they're drawn, so a store event that redraws the window shows them changed; what the store
// doesn't keep (the machine's recent load, the burn's readings, today's totals) their owners gather from its events.

// --- Sessions (the session tabs) ---

// sessionsPanel is every session by status: how many are open and need you, the split as a strip, and the most
// urgent five with why; a click brings one to the front.
func (t *sessionTabs) sessionsPanel(c *ui.Ctx, h plugin.Host) {
	th := c.Theme()
	now := c.Now()
	var ss []core.SessionState
	for _, s := range h.State().Sessions {
		if !s.Archived {
			ss = append(ss, s)
		}
	}
	slices.SortStableFunc(ss, func(a, b core.SessionState) int {
		return slices.Index(kit.StatusOrder, a.Status) - slices.Index(kit.StatusOrder, b.Status)
	})
	need := 0
	counts := map[core.Status]int{}
	for _, s := range ss {
		counts[s.Status]++
		if s.Status == core.StatusNeedsYou || s.Status == core.StatusPermission {
			need++
		}
	}
	c.Row("big", ui.Style{Gap: 14, Cross: ui.End}, func(c *ui.Ctx) {
		for _, f := range []struct {
			key  string
			n    int
			unit string
			tone render.Color
		}{{"open", len(ss), "open", th.C(theme.Fg)}, {"need", need, "need you", th.C(theme.StatusNeedsYou)}} {
			c.Row(f.key, ui.Style{Gap: 4, Cross: ui.End, Role: ui.RoleText, Label: fmt.Sprint(f.n, " ", f.unit)}, func(c *ui.Ctx) {
				c.Text("n", fmt.Sprint(f.n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 26, Weight: typeset.Medium}, Color: f.tone})
				c.Text("u", f.unit, kit.MonoFont(11, th.C(theme.FgFaint)))
			})
		}
	})
	var strip []widget.ChartSeries
	for _, s := range kit.StatusOrder {
		if n := counts[s]; n > 0 {
			strip = append(strip, widget.ChartSeries{Name: kit.StatusName(s), Values: []float64{float64(n)}, Tone: th.C(kit.StatusTokens[s])})
		}
	}
	if len(strip) > 0 {
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Height: 8, Label: "Sessions by status"})
	}
	for _, s := range ss[:min(len(ss), 5)] {
		if widget.Clickable(c, s.ID, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(19), Gap: 8, Cross: ui.Center, Label: kit.Label(s),
			Help: kit.HUDHelp + "#sessions"}, func(c *ui.Ctx) {
			kit.HUDDot(c, "dot", th.C(kit.StatusTokens[s.Status]))
			c.Text("n", kit.Label(s), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("w", kit.Why(s, now), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		}).Clicked {
			h.Run(context.Background(), "session.focus", registry.Args{"id": string(s.ID)})
		}
	}
}
