package shell

import (
	"context"
	"fmt"
	"log/slog"
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

var tHUDLogSource = theme.Declare("hud.log.source", "violet")

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

// --- Logs (the notices) ---

// logsPanel is the log's tail, newest first: info and up, from every source.
func (n *notices) logsPanel(c *ui.Ctx) {
	th := c.Theme()
	var rows []logRow // newest first
	for _, r := range slices.Backward(n.panel.rows(n.h.State())) {
		if len(rows) == 11 {
			break
		}
		if bucket(r.level) >= slog.LevelInfo {
			rows = append(rows, r)
		}
	}
	c.Row("chips", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		widget.Chip(c, "all", "all processes", th.C(theme.FgMuted), "")
		widget.Chip(c, "lvl", "info+", th.C(theme.FgMuted), "")
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
	})
	tags := map[slog.Level]struct {
		tag string
		tok theme.TokenID
	}{slog.LevelError: {"ERR", theme.Error}, slog.LevelWarn: {"WRN", theme.Warn}, slog.LevelInfo: {"INF", theme.StatusWorking}}
	c.Column("lines", ui.Style{W: ui.Flex(1), Gap: 1, Clip: true}, func(c *ui.Ctx) {
		for i, r := range rows {
			tg := tags[bucket(r.level)]
			c.Row(len(rows)-1-i, ui.Style{W: ui.Flex(1), H: ui.Px(16), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: r.plain()}, func(c *ui.Ctx) {
				c.Text("t", r.time.Local().Format("15:04:05.000"), kit.MonoFont(10.5, th.C(theme.FgGhost)))
				c.Text("l", tg.tag, kit.Mono(10.5, typeset.SemiBold, th.C(tg.tok)))
				c.Row("s", ui.Style{W: ui.Px(110), Clip: true}, func(c *ui.Ctx) {
					c.Text("s", where(r), kit.MonoFont(10.5, th.C(tHUDLogSource)))
				})
				c.Row("m", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("m", kit.FirstLine(r.text), kit.MonoFont(10.5, th.C(theme.FgMuted)))
				})
			})
		}
	})
	c.Row("foot", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Link(c, "open", "Open the log ↗", kit.HUDHelp+"#logs") {
			n.h.Run(context.Background(), "Log", nil)
		}
	})
}
