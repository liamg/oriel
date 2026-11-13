package stats

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The stats plugin's HUD panels: plan usage and today's totals.

var tHUDDivider = theme.Declare("hud.divider", "line")

// hudFigure is a big number with its label over it and its unit after: TOKENS 4.82M.
func hudFigure(c *ui.Ctx, key any, label, value, unit string, tone render.Color) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 3, Role: ui.RoleText, Label: label + " " + value + unit}, func(c *ui.Ctx) {
		c.Text("l", label, kit.KickerFont(th.C(theme.FgFaint)))
		c.Row("v", ui.Style{Gap: 3, Cross: ui.End}, func(c *ui.Ctx) {
			c.Text("n", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 20, Weight: typeset.Medium}, Color: tone})
			if unit != "" {
				c.Text("u", unit, kit.MonoFont(11, th.C(theme.FgFaint)))
			}
		})
	})
}

// hudDivider is the dashed rule between a panel's parts.
func hudDivider(c *ui.Ctx, key any) {
	tone := c.Theme().C(tHUDDivider)
	c.Paint(key, ui.Flex(1), ui.Px(1), func(f render.Frame, r render.Rect) {
		var p render.Path
		for x := r.Min.X; x < r.Max.X; x += 6 {
			p.MoveTo(render.Point{X: x, Y: r.Min.Y}).LineTo(render.Point{X: min(x+3, r.Max.X), Y: r.Min.Y})
		}
		f.Path(&p, render.Style{Stroke: tone, Width: 1})
	})
}

// usagePanel is every account's windows, the account in front first, each with where its burn takes it by the reset.
func (x *Plugin) usagePanel(c *ui.Ctx) {
	th := c.Theme()
	now := x.now()
	us, failed := x.hudUsage()
	for i, f := range failed {
		c.Row(fmt.Sprint("failed", i), ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(registry.ProviderTitle(x.h, f.Provider)), kit.Mono(10, typeset.Medium, th.C(theme.FgFaint)))
			c.Row("e", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				c.Text("e", "plan couldn't be read", kit.MonoFont(10.5, th.C(theme.Warn)))
			})
		})
	}
	if len(us) == 0 && len(failed) > 0 {
		return
	}
	if len(us) == 0 {
		if st := x.reading.State(); st.Phase == widget.Pending {
			widget.Busy(c, "none", st, widget.BusyOpts{Label: "Reading the plans", Size: 10.5, Help: "agent/usage"})
			return
		}
		c.Text("none", "no plan read yet", kit.MonoFont(10.5, th.C(theme.FgGhost)))
		return
	}
	for i, u := range us {
		if i > 0 {
			hudDivider(c, fmt.Sprint("d", i))
		}
		hist := x.usageHistory(u)
		c.Column(i, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			c.Row("h", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				parts := []string{strings.ToUpper(registry.ProviderTitle(x.h, u.Provider)), x.accountLabel(u.Account, u.AccountName)}
				if u.Tier != "" {
					parts = append(parts, strings.ToUpper(u.Tier))
				}
				c.Text("t", strings.Join(parts, " · "), kit.Mono(10, typeset.Medium, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if len(x.h.State().SignedOut(u)) > 0 {
					widget.Chip(c, "out", "signed out", th.C(theme.Error), "")
				}
				widget.Chip(c, "p", u.Provider, th.C(theme.Accent), "")
			})
			var foot []string
			for j, w := range u.Windows {
				if qualifier(w) != "" {
					continue
				}
				r := readWindow(now, w, hist)
				tone := windowTone(th, w)
				c.Row(j, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Row("k", ui.Style{W: ui.Px(22)}, func(c *ui.Ctx) { c.Text("t", usageShort(w), kit.MonoFont(10.5, th.C(theme.FgMuted))) })
					widget.Meter(c, "m", w.Used, widget.MeterOpts{Tone: tone, Height: 6, To: r.atReset, Label: w.Name})
					fg := th.C(theme.Fg)
					if w.Used >= .7 {
						fg = th.C(theme.Warn)
					}
					c.Text("v", fmt.Sprintf("%d%%", kit.Pct(w.Used)), kit.Mono(10.5, typeset.SemiBold, fg))
					c.Text("at", x.headed(r, now), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				})
				if fiveHour(w) {
					if !w.Resets.IsZero() {
						foot = append(foot, "resets "+strings.ReplaceAll(kit.Duration(w.Resets.Sub(now)), " ", ""))
					}
					foot = append(foot, fmt.Sprintf("burn %.1f%%/h", r.burn*100))
				} else if !w.Resets.IsZero() {
					foot = append(foot, "resets "+w.Resets.In(x.Loc).Format("Mon 15:04"))
				}
			}
			if len(foot) > 0 {
				c.Row("foot", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 32}, Main: ui.SpaceBetween}, func(c *ui.Ctx) {
					for k, s := range foot {
						c.Text(k, s, kit.MonoFont(9.5, th.C(theme.FgGhost)))
					}
				})
			}
		})
	}
}

// headed is where a window's burn takes it, shortly: "→59% at reset", "→97% Thu", "idle", "full Thu 14:38".
func (x *Plugin) headed(r windowRead, now time.Time) string {
	switch {
	case !r.runsOut.IsZero():
		return "full " + r.runsOut.In(x.Loc).Format("Mon 15:04")
	case r.burn == 0:
		return "idle"
	case fiveHour(r.w):
		return fmt.Sprintf("→%d%% at reset", kit.Pct(r.atReset))
	case !r.w.Resets.IsZero() && r.w.Resets.Sub(now) > 20*time.Hour:
		return fmt.Sprintf("→%d%% %s", kit.Pct(r.atReset), r.w.Resets.In(x.Loc).Format("Mon"))
	}
	return fmt.Sprintf("→%d%%", kit.Pct(r.atReset))
}

// usageNote is the panel's note: how many accounts, and how long ago they were read.
func (x *Plugin) usageNote() string {
	us := x.h.State().Usage
	var read time.Time
	for _, u := range us {
		if u.Sampled.After(read) {
			read = u.Sampled
		}
	}
	if len(us) == 0 {
		return ""
	}
	return kit.Counted(len(us), "account") + " · read " + widget.AgoLong(x.now(), read)
}

// todayPanel is today's totals: tokens, cost, the cache's share and the requests opened today; then the hud.today
// slot's indicators.
func (x *Plugin) todayPanel(c *ui.Ctx) {
	th := c.Theme()
	now := x.now().In(x.Loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.Loc)
	r, _ := x.report(stats.Query{From: from, To: from.AddDate(0, 0, 1), Offset: x.offset(), Split: true})
	tok := r.Totals.Tokens
	cache := 0.0
	if p := tok.Prompt(); p > 0 {
		cache = float64(tok.CacheRead) / float64(p)
	}
	st := x.h.State()
	threads := 0
	for _, s := range st.Sessions {
		for _, t := range s.Threads {
			if !t.Created.Before(from) {
				threads++
			}
		}
	}
	total := kit.Tokens(tok.Total())
	unit := ""
	if n := len(total); n > 0 && (total[n-1] == 'M' || total[n-1] == 'k') {
		total, unit = total[:n-1], total[n-1:]
	}
	dollars, cents, _ := strings.Cut(fmt.Sprintf("%.2f", r.Totals.Cost), ".")
	c.Row("a", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		hudFigure(c, "tokens", "TOKENS", total, unit, th.C(theme.Fg))
		hudFigure(c, "cost", "COST", "$"+dollars, "."+cents, th.C(theme.Fg))
		hudFigure(c, "cache", "CACHE", fmt.Sprint(kit.Pct(cache)), "%", th.C(theme.Fg))
	})
	c.Row("b", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		hudFigure(c, "threads", "THREADS", fmt.Sprint(threads), "", th.C(theme.Fg))
	})
	inds := uireg.Indicators(x.h, uireg.SlotHUDToday)
	if is, ok := kit.IndicatorsOf(x.h); ok && len(inds) > 0 {
		hudDivider(c, "div")
		for _, ind := range inds {
			if n := is.Node(c, ind); n != nil {
				c.Row(ind.Name, ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(n) })
			}
		}
	}
}

// usedWithin is how recently a provider must have run a session for the HUD to show its plan.
const usedWithin = 14 * 24 * time.Hour

// hudUsage are the plans the HUD shows, the account in front first: those of the providers
// sessions have run on lately, when any have; every plan read, when none have. failed are the providers in use whose
// plan couldn't be read, so one that didn't read isn't simply missing.
func (x *Plugin) hudUsage() (us []core.ProviderUsage, failed []core.UsageFailure) {
	st := x.h.State()
	front, _ := x.frontUsage()
	used := usedProviders(st, x.now())
	for _, u := range st.Usage {
		if len(used) == 0 || used[u.Provider] {
			us = append(us, u)
		}
	}
	slices.SortStableFunc(us, func(a, b core.ProviderUsage) int {
		switch {
		case a.Same(front):
			return -1
		case b.Same(front):
			return 1
		}
		return 0
	})
	for _, f := range st.UsageFailures {
		if used[f.Provider] && !slices.ContainsFunc(us, func(u core.ProviderUsage) bool { return u.Provider == f.Provider }) {
			failed = append(failed, f)
		}
	}
	return us, failed
}

// usedProviders are the providers a session has run on within usedWithin of now.
func usedProviders(st core.State, now time.Time) map[string]bool {
	out := map[string]bool{}
	for _, se := range st.Sessions {
		p := se.Agent.Provider
		if p == "" {
			continue
		}
		ats := []time.Time{se.Created, se.Agent.TurnStarted}
		for _, a := range se.Agent.Accounts {
			ats = append(ats, a.At)
		}
		if last := slices.MaxFunc(ats, time.Time.Compare); now.Sub(last) < usedWithin {
			out[p] = true
		}
	}
	return out
}
