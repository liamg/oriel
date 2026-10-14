package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker/stats"
)

// The Usage page (43's panel 5) and the title bar's indicators: each provider's plan windows, how fast they're
// going, a week of history, and which repos burned the window.

var (
	tUsageOK    = theme.Declare("usage.ok", "green")
	tUsageTight = theme.Declare("usage.tight", "amber")
	tUsageOut   = theme.Declare("usage.out", "red")
	tUsageFive  = theme.DeclareOn("usage.five_hour", "alpha(accent, 40%)", "bg.surface")
	tUsageWeek  = theme.Declare("usage.weekly", "amber")
	tUsageModel = theme.Declare("usage.weekly.model", "violet")
	tUsageHatch = theme.DeclareOn("usage.projection", "alpha(fg, 18%)", "bg.surface")
)

// reading is a window's share used at a time.
type reading struct {
	at   time.Time
	used float64
}

// windowRead is a plan window as the page reads it: how fast it's going and where that takes it.
type windowRead struct {
	w       core.UsageWindow
	burn    float64 // share per hour over basis
	basis   time.Duration
	overall float64   // share per hour since it opened
	atReset float64   // where the burn takes it by the reset
	runsOut time.Time // when it reaches all of it before the reset; zero if it doesn't
}

// fiveHour says a window is the short one: its burn is read over 30 minutes, not a day.
func fiveHour(w core.UsageWindow) bool {
	if w.Length > 0 {
		return w.Length <= 6*time.Hour
	}
	n := strings.ToLower(w.Name)
	return strings.HasPrefix(n, "5h") || strings.Contains(n, "session") || strings.Contains(n, "5-hour")
}

// samples are a window's readings in hist for the period it's in now: the same name, resetting at the same time.
func samples(w core.UsageWindow, hist []core.ProviderUsage) []reading {
	var out []reading
	for _, u := range hist {
		for _, v := range u.Windows {
			if v.Name == w.Name && (w.Resets.IsZero() || absDur(v.Resets.Sub(w.Resets)) < 2*time.Minute) {
				out = append(out, reading{u.Sampled, v.Used})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b reading) int { return a.at.Compare(b.at) })
	return out
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// usedAt is the share used at t: the last reading at or before it, 0 before the first (the window opened at 0).
func usedAt(ss []reading, t time.Time) float64 {
	u := 0.0
	for _, s := range ss {
		if s.at.After(t) {
			break
		}
		u = s.used
	}
	return u
}

// readWindow is a window now, its burn the slope over the last 30 minutes (the 5-hour window) or 24 hours.
func readWindow(now time.Time, w core.UsageWindow, hist []core.ProviderUsage) windowRead {
	r := windowRead{w: w, basis: 24 * time.Hour}
	if fiveHour(w) {
		r.basis = 30 * time.Minute
	}
	ss := samples(w, hist)
	from := now.Add(-r.basis)
	opened := w.Opened()
	if !opened.IsZero() && opened.After(from) {
		from = opened
	}
	if len(ss) > 0 && opened.IsZero() && ss[0].at.After(from) {
		from = ss[0].at
	}
	if h := now.Sub(from).Hours(); h > 0 {
		r.burn = max(0, (w.Used-usedAt(ss, from))/h)
	}
	if !opened.IsZero() {
		if h := now.Sub(opened).Hours(); h > 0 {
			r.overall = w.Used / h
		}
	}
	r.atReset = w.Used
	if !w.Resets.IsZero() && w.Resets.After(now) {
		r.atReset += r.burn * w.Resets.Sub(now).Hours()
		if r.burn > 0 && r.atReset >= 1 {
			r.runsOut = now.Add(time.Duration((1 - w.Used) / r.burn * float64(time.Hour)))
		}
	}
	return r
}

// verdict is where a window's burn takes it, in words: "≈ 71% at the reset · won't run out", or when it runs out.
func (r windowRead) verdict(now time.Time, loc *time.Location) (string, theme.TokenID) {
	switch {
	case !r.runsOut.IsZero():
		return fmt.Sprintf("at this rate the %s window runs out %s · %s before the reset", strings.ToLower(windowKind(r.w)),
			r.runsOut.In(loc).Format("Mon 15:04"), strings.ReplaceAll(hm(r.w.Resets.Sub(r.runsOut)), " 00m", "")), tUsageOut
	case r.atReset >= .9:
		return fmt.Sprintf("≈ %d%% at the reset · tight", pct(r.atReset)), tUsageTight
	}
	return fmt.Sprintf("≈ %d%% at the reset · won't run out", pct(r.atReset)), tUsageOK
}

// windowKind is a window's length in words: 5-hour, weekly.
func windowKind(w core.UsageWindow) string {
	switch {
	case fiveHour(w):
		return "5-hour"
	case w.Length >= 6*24*time.Hour || strings.Contains(strings.ToLower(w.Name), "week"):
		return "weekly"
	}
	return w.Name
}

// windowLabel is a window as its card names it: 5-HOUR WINDOW, WEEKLY · OPUS.
func windowLabel(w core.UsageWindow) string {
	base := strings.ToUpper(windowKind(w))
	if fiveHour(w) {
		base += " WINDOW"
	}
	if q := qualifier(w); q != "" {
		base += " · " + strings.ToUpper(q)
	}
	return base
}

// qualifier is a window's model, when it's for one: "Current week (Opus)" is opus's.
func qualifier(w core.UsageWindow) string {
	if i, j := strings.Index(w.Name, "("), strings.LastIndex(w.Name, ")"); i >= 0 && j > i {
		if q := strings.ToLower(w.Name[i+1 : j]); q != "all models" {
			return q
		}
	}
	return ""
}

// usageShort is a window's short name, for the title bar: 5H, WK.
func usageShort(w core.UsageWindow) string {
	switch windowKind(w) {
	case "5-hour":
		return "5H"
	case "weekly":
		if q := qualifier(w); q != "" {
			return strings.ToUpper(q)
		}
		return "WK"
	}
	return strings.ToUpper(firstLine(w.Name))
}

func windowTone(th *theme.Resolved, w core.UsageWindow) render.Color {
	switch {
	case fiveHour(w):
		return th.C(tStatsHours)
	case qualifier(w) != "":
		return th.C(tUsageModel)
	}
	return th.C(tUsageWeek)
}

// resets is when a window resets, and how long until.
func resets(now time.Time, loc *time.Location, w core.UsageWindow) (string, string) {
	if w.Resets.IsZero() {
		return "", ""
	}
	at := w.Resets.In(loc)
	f := "15:04"
	if w.Resets.Sub(now) > 20*time.Hour {
		f = "Mon 15:04"
	}
	return "resets " + at.Format(f), "in " + strings.ReplaceAll(hm(w.Resets.Sub(now)), " 00m", "")
}

// indicators are the title bar's: the plan of the session in front's provider, its windows as meters, one button
// that opens Usage, with every provider's windows in a card under the pointer.
func (x *statsExt) indicators(c *ui.Ctx) {
	u, ok := x.frontUsage()
	if !ok {
		return
	}
	th := c.Theme()
	anchor := c.Next("usage")
	if widget.Clickable(c, "usage", ui.Style{Dir: ui.Row, H: ui.Px(24), Pad: ui.PadXY(8, 0), Gap: 8, Cross: ui.Center, Radius: 5,
		Stroke: th.C(theme.LineStrong), Border: 1, Role: ui.RoleButton, Label: cmpOr(u.Plan, providerName(u.Provider)) + " usage",
		Help: "agent/usage#indicators"}, func(c *ui.Ctx) {
		for i, w := range u.Windows {
			if qualifier(w) != "" {
				continue
			}
			tone := th.C(tMeterContext)
			if w.Used >= .9 {
				tone = th.C(theme.Error)
			}
			c.Text(fmt.Sprint("n", i), usageShort(w), monoFont(10.5, th.C(theme.FgFaint)))
			widget.Meter(c, fmt.Sprint("m", i), w.Used, widget.MeterOpts{Tone: tone, Width: ui.Px(44), Label: w.Name})
			c.Text(fmt.Sprint("v", i), fmt.Sprintf("%d%%", pct(w.Used)), monoFont(10.5, th.C(theme.Fg)))
		}
	}).Clicked {
		x.run("usage.open", nil)
	}
	widget.HoverCard(c, "card", anchor, widget.HoverCardOpts{Label: "Plan usage", Help: "agent/usage#indicators", Width: 430}, func(c *ui.Ctx) {
		x.usageCard(c, u)
	})
}

// usageCard is every provider's windows, the session in front's first: what the title bar's card shows.
func (x *statsExt) usageCard(c *ui.Ctx, front core.ProviderUsage) {
	th := c.Theme()
	now := x.now()
	us := slices.Clone(x.h.State().Usage)
	slices.SortStableFunc(us, func(a, b core.ProviderUsage) int {
		switch {
		case a.Same(front):
			return -1
		case b.Same(front):
			return 1
		}
		return 0
	})
	var read time.Time
	for i, u := range us {
		if u.Sampled.After(read) {
			read = u.Sampled
		}
		hist := x.usageHistory(u)
		c.Row(fmt.Sprint("p", i), ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			providerMark(c, u.Provider)
			c.Text("n", cmpOr(u.Plan, providerName(u.Provider)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
			sub := x.accountLabel(u.Account, u.AccountName)
			if u.Same(front) {
				sub += " · this session's account"
			}
			c.Text("s", sub, monoFont(10, th.C(theme.FgFaint)))
		})
		for j, w := range u.Windows {
			r := readWindow(now, w, hist)
			c.Row(fmt.Sprint("w", i, j), ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Row("k", ui.Style{W: ui.Px(34)}, func(c *ui.Ctx) { c.Text("t", usageShort(w), monoFont(10, th.C(theme.FgGhost))) })
				widget.Meter(c, "m", w.Used, widget.MeterOpts{Tone: windowTone(th, w), Width: ui.Px(60), Label: w.Name})
				at, in := resets(now, x.loc, w)
				line := fmt.Sprintf("%d%%  %s", pct(w.Used), at)
				if in != "" && fiveHour(w) {
					line += " · " + in
				}
				c.Text("v", line, monoFont(10.5, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("b", fmt.Sprintf("%.1f%%/h", r.burn*100), monoFont(10, th.C(theme.FgFaint)))
			})
			if !r.runsOut.IsZero() {
				v, tone := r.verdict(now, x.loc)
				c.Row(fmt.Sprint("x", i, j), ui.Style{Pad: ui.Insets{L: 42}}, func(c *ui.Ctx) { c.Text("t", v, monoFont(10, th.C(tone))) })
			}
		}
	}
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "click for Usage", monoFont(10, th.C(theme.FgFaint)))
		keycap(c, "k", boundKey(x.h, "usage.open", nil))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if !read.IsZero() {
			c.Text("r", "read "+agoText(now, read)+" · no tokens spent", monoFont(10, th.C(theme.FgFaint)))
		}
	})
}

func agoText(now, t time.Time) string {
	if a := ago(now, t); a != "now" {
		return a + " ago"
	}
	return "just now"
}

// providerMark is a provider's small square: its initial.
func providerMark(c *ui.Ctx, provider string) {
	th := c.Theme()
	letters, tone := strings.ToUpper(provider), th.C(theme.FgMuted)
	switch provider {
	case "claude":
		letters, tone = "C", th.C(tUsageWeek)
	case "codex":
		letters = "CX"
	default:
		letters = projectInitials(provider)
	}
	c.Box("mark", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 4, Fill: theme.WithAlpha(tone, .85), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", letters, mono(8.5, typeset.Bold, th.C(theme.BgBase)))
	})
}

func (x *statsExt) usagePage(c *ui.Ctx) {
	if !x.usageOpen || c.Theme().IsProbe() {
		return
	}
	widget.Page(c, "usage", &x.usageOpen, widget.PageOpts{Over: mainArea(), KeyMode: UsageKeyMode, Label: "Usage", Help: "agent/usage"},
		func(c *ui.Ctx) {
			th := c.Theme()
			st := x.h.State()
			now := x.now()
			var read time.Time
			for _, u := range st.Usage {
				if u.Sampled.After(read) {
					read = u.Sampled
				}
			}
			c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
				c.Text("h", "USAGE", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
					Color: th.C(theme.Fg)})
				c.Text("m", fmt.Sprintf("plan windows · %s · %s · %s", counted(len(st.Usage), "account"), counted(len(st.Workers), "worker"),
					now.In(x.loc).Format("Mon Jan 2 15:04")), monoFont(11, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if !read.IsZero() {
					c.Text("read", "read "+agoText(now, read), monoFont(11, th.C(theme.FgFaint)))
				}
				if widget.Button(c, "refresh", "Refresh", widget.ButtonOpts{Key: "r", Help: "agent/usage#burned"}) {
					x.run("usage.refresh", nil)
				}
				if widget.Button(c, "stats", "Stats", widget.ButtonOpts{Help: "agent/stats#page"}) {
					x.run("stats.open", nil)
				}
				if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "agent/usage"}) {
					x.usageOpen = false
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 14}, func(c *ui.Ctx) {
				if len(st.Usage) == 0 {
					note(c, "none", "No plan windows read yet: they come with a session's first turn, or Refresh.")
				}
				if u, ok := x.usageAccount(); ok {
					x.accountSwitch(c, u)
					x.provider(c, 0, u, now)
				}
				c.Row("how", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 9), Gap: 6, Radius: 6, Stroke: th.C(theme.LineStrong), Border: 1}, func(c *ui.Ctx) {
					c.Text("t", "How it's measured", monoFont(10.5, th.C(theme.Fg)))
					note(c, "n", "the worker reads each provider's own figures; nothing is prompted, so reading spends no tokens · burn is the slope over the last 30 min (5h) or 24h (weekly)")
				})
			})
		})
}

// provider is one provider's section: its windows, their week, and what burned the weekly one.
func (x *statsExt) provider(c *ui.Ctx, i int, u core.ProviderUsage, now time.Time) {
	th := c.Theme()
	hist := x.usageHistory(u)
	c.Column(fmt.Sprint("prov", i), ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleGroup, Label: cmpOr(u.Plan, providerName(u.Provider))}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			providerMark(c, u.Provider)
			c.Text("n", cmpOr(u.Plan, providerName(u.Provider)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 15, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
			c.Text("acct", x.accountLabel(u.Account, u.AccountName), mono(10.5, typeset.SemiBold, th.C(theme.FgMuted)))
			c.Text("m", x.providerLine(u), monoFont(10.5, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if u.Source != "" {
				c.Text("src", "windows from "+u.Source+" · "+agoText(now, u.Sampled), monoFont(10, th.C(theme.FgGhost)))
			}
		})
		c.Row("windows", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			for j, w := range u.Windows {
				x.windowCard(c, j, readWindow(now, w, hist), now)
			}
		})
		c.Row("week", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			x.weekChart(c, u, hist, now)
			x.burnedCard(c, u, hist, now)
		})
	})
}

// providerLine is the models and sessions an account runs: "opus-4.1 · sonnet-4.5 · 4 sessions on mbp and devbox".
func (x *statsExt) providerLine(u core.ProviderUsage) string {
	var models, workers []string
	n := 0
	for _, se := range x.h.State().Sessions {
		wid := se.Worker
		if wid == "" {
			wid = core.LocalWorker
		}
		if se.Agent.Provider != u.Provider || se.Archived || !u.Uses(wid) {
			continue
		}
		n++
		if m := se.Agent.Model; m != "" && !slices.Contains(models, m) {
			models = append(models, m)
		}
		if w := x.workerName(wid); !slices.Contains(workers, w) {
			workers = append(workers, w)
		}
	}
	parts := models
	if n > 0 {
		parts = append(parts, counted(n, "session")+" on "+strings.Join(workers, " and "))
	}
	return strings.Join(parts, " · ")
}

func (x *statsExt) windowCard(c *ui.Ctx, key int, r windowRead, now time.Time) {
	th := c.Theme()
	w := r.w
	line := th.C(tStatsCardLine)
	if !r.runsOut.IsZero() {
		line = theme.WithAlpha(th.C(tUsageOut), .6)
	}
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 8, Fill: th.C(tStatsCard), Stroke: line, Border: 1, Radius: 8, Role: ui.RoleGroup,
		Label: windowLabel(w), Help: "agent/usage#windows"}, func(c *ui.Ctx) {
		c.Row("hd", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", windowLabel(w), capText(th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			at, in := resets(now, x.loc, w)
			c.Text("r", at, monoFont(10.5, th.C(theme.Fg)))
			if in != "" {
				c.Text("in", "· "+in, monoFont(10.5, th.C(theme.FgGhost)))
			}
		})
		tone := windowTone(th, w)
		if !r.runsOut.IsZero() {
			tone = th.C(tUsageWeek)
		}
		c.Row("v", ui.Style{Gap: 8, Cross: ui.End}, func(c *ui.Ctx) {
			c.Text("p", fmt.Sprintf("%d%%", pct(w.Used)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 28, Weight: typeset.SemiBold},
				Color: tone})
			sub := "used"
			if o := w.Opened(); !o.IsZero() {
				if fiveHour(w) {
					sub += " · opened " + o.In(x.loc).Format("15:04")
				} else {
					sub += " · since " + o.In(x.loc).Format("Mon Jan 2 15:04")
				}
			}
			if q := qualifier(w); q != "" {
				sub = "used · " + q + " only"
			}
			c.Text("s", sub, monoFont(10.5, th.C(theme.FgFaint)))
		})
		projected(c, w.Used, r.atReset, tone, th.C(tUsageOut), !r.runsOut.IsZero())
		c.Row("scale", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			note(c, "0", "0")
			c.Box("g1", ui.Style{W: ui.Flex(1)}, nil)
			if !r.runsOut.IsZero() {
				c.Text("full", "full "+r.runsOut.In(x.loc).Format("Mon 15:04"), monoFont(10, th.C(tUsageOut)))
			} else {
				note(c, "at", fmt.Sprintf("at reset ≈ %d", pct(r.atReset)))
				c.Box("g2", ui.Style{W: ui.Flex(1)}, nil)
				note(c, "100", "100")
			}
		})
		c.Row("burn", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "BURN", capText(th.C(theme.FgGhost)))
			basis := "the last 30 min"
			if r.basis >= 24*time.Hour {
				basis = "the last 24h"
			}
			c.Text("v", fmt.Sprintf("%.1f%%/h", r.burn*100), monoFont(11, th.C(theme.Fg)))
			c.Text("b", "over "+basis, monoFont(10.5, th.C(theme.FgFaint)))
			if !fiveHour(w) && r.overall > 0 {
				c.Text("o", fmt.Sprintf("· %.1f%%/h over the week", r.overall*100), monoFont(10.5, th.C(theme.FgFaint)))
			}
		})
		v, vt := r.verdict(now, x.loc)
		box := c.Next("verdict")
		width := float32(280)
		if rect, ok := c.Rect(box); ok {
			width = rect.Dx() - 20
		}
		c.Row("verdict", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Radius: 5, Fill: theme.WithAlpha(th.C(vt), .07), Stroke: theme.WithAlpha(th.C(vt), .35),
			Border: 1, Role: ui.RoleStatus, Label: v}, func(c *ui.Ctx) {
			widget.Wrap(c, "t", v, width, monoFont(10.5, th.C(vt)))
		})
	})
}

// projected is a window's meter: what's used, then hatched to where the burn takes it by the reset.
func projected(c *ui.Ctx, used, at float64, tone, out render.Color, runsOut bool) {
	th := c.Theme()
	hatch := th.C(tUsageHatch)
	if runsOut {
		hatch = theme.WithAlpha(out, .5)
	}
	track := th.C(tStatsTrack)
	c.Paint("meter", ui.Flex(1), ui.Px(10), func(f render.Frame, r render.Rect) {
		f.Rect(r, render.Style{Fill: track, Radius: 2})
		u := r.Min.X + r.Dx()*float32(min(max(used, 0), 1))
		a := r.Min.X + r.Dx()*float32(min(max(at, used), 1))
		if a > u {
			pr := render.Rect{Min: render.Point{X: u, Y: r.Min.Y}, Max: render.Point{X: a, Y: r.Max.Y}}
			f.PushClip(pr, 0)
			var p render.Path
			for x := pr.Min.X - r.Dy(); x < pr.Max.X; x += 5 {
				p.MoveTo(render.Point{X: x, Y: r.Max.Y}).LineTo(render.Point{X: x + r.Dy(), Y: r.Min.Y})
			}
			f.Path(&p, render.Style{Stroke: hatch, Width: 1.5})
			f.Pop()
		}
		if u > r.Min.X {
			f.Rect(render.Rect{Min: r.Min, Max: render.Point{X: u, Y: r.Max.Y}}, render.Style{Fill: tone, Radius: 2})
		}
		f.Rect(render.XYWH(u-1, r.Min.Y-2, 2, r.Dy()+4), render.Style{Fill: th.C(theme.Fg)})
	})
}

// weekChart is the provider's windows over the last 7 days, hour by hour: the 5-hour window's peak in each as
// bars, the weekly ones as lines, dashed on to the reset at their burn.
func (x *statsExt) weekChart(c *ui.Ctx, u core.ProviderUsage, hist []core.ProviderUsage, now time.Time) {
	th := c.Theme()
	start := now.Truncate(time.Hour).Add(-6*24*time.Hour - 12*time.Hour)
	end := now.Truncate(time.Hour).Add(time.Hour)
	for _, w := range u.Windows {
		if !fiveHour(w) && w.Resets.After(end) && w.Resets.Sub(now) < 3*24*time.Hour {
			end = w.Resets.Truncate(time.Hour).Add(time.Hour)
		}
	}
	n := int(end.Sub(start).Hours()) + 1
	at := func(t time.Time) float64 { return t.Sub(start).Hours() }
	nowAt := at(now)
	var series []widget.ChartSeries
	var marks []widget.ChartMark
	var ticks []float64
	for _, w := range u.Windows {
		name := strings.ToLower(windowLabel(w))
		tone := windowTone(th, w)
		vals := make([]float64, n)
		if fiveHour(w) {
			type period struct {
				resets time.Time
				peak   float64
			}
			var ps []period
			for _, h := range hist {
				for _, v := range h.Windows {
					if v.Name != w.Name {
						continue
					}
					if len(ps) == 0 || absDur(ps[len(ps)-1].resets.Sub(v.Resets)) > 2*time.Minute {
						ps = append(ps, period{resets: v.Resets})
					}
					ps[len(ps)-1].peak = max(ps[len(ps)-1].peak, v.Used)
				}
			}
			for _, p := range ps {
				o := p.resets.Add(-max(w.Length, 5*time.Hour))
				for i := max(int(at(o)), 0); i < min(int(at(p.resets)), n); i++ {
					if float64(i) <= nowAt {
						vals[i] = p.peak * 100
					}
				}
				if r := at(p.resets); r >= 0 && r < float64(n) {
					ticks = append(ticks, r)
				}
			}
			series = append(series, widget.ChartSeries{Name: "5-hour, peak per window", Values: vals, Tone: th.C(tUsageFive), Kind: widget.ChartBars})
			continue
		}
		var prev []reading
		for _, h := range hist {
			for _, v := range h.Windows {
				if v.Name == w.Name {
					prev = append(prev, reading{h.Sampled, v.Used})
				}
			}
		}
		for i := range vals {
			t := start.Add(time.Duration(i) * time.Hour)
			if t.After(now) || len(prev) == 0 || t.Before(prev[0].at) {
				vals[i] = math.NaN()
				continue
			}
			vals[i] = usedAt(prev, t) * 100
		}
		if i := int(nowAt); i >= 0 && i < n {
			vals[i] = w.Used * 100
		}
		series = append(series, widget.ChartSeries{Name: name, Values: vals, Tone: tone, Kind: widget.ChartLine})
		r := readWindow(now, w, hist)
		proj := make([]float64, n)
		for i := range proj {
			proj[i] = math.NaN()
		}
		from := int(nowAt)
		for i := from; i < n; i++ {
			t := start.Add(time.Duration(i) * time.Hour)
			if !w.Resets.IsZero() && t.After(w.Resets) {
				break
			}
			proj[i] = min(100, (w.Used+r.burn*max(0, t.Sub(now).Hours()))*100)
		}
		series = append(series, widget.ChartSeries{Name: name + " at this rate", Values: proj, Tone: tone, Kind: widget.ChartLine, Dashed: true, From: from})
		if !w.Resets.IsZero() && qualifier(w) == "" {
			o := w.Opened()
			if a := at(o); !o.IsZero() && a >= 0 && a < float64(n) {
				marks = append(marks, widget.ChartMark{At: a, Label: "↺ weekly · " + o.In(x.loc).Format("Mon 15:04"), Tone: th.C(tStatsHours), Dashed: true})
			}
		}
		if !r.runsOut.IsZero() && qualifier(w) == "" {
			marks = append(marks, widget.ChartMark{At: at(r.runsOut), Label: "full " + r.runsOut.In(x.loc).Format("Mon 15:04"), Tone: th.C(tUsageOut),
				Dashed: true})
		}
	}
	marks = append(marks, widget.ChartMark{At: nowAt, Label: "now"})
	labels := make([]string, n)
	for i := range labels {
		t := start.Add(time.Duration(i) * time.Hour).In(x.loc)
		if t.Hour() == 12 {
			labels[i] = t.Format("Mon 2")
		}
	}
	legend := series[:0:0]
	for _, s := range series {
		if !s.Dashed {
			legend = append(legend, s)
		}
	}
	statsCard(c, "week", ui.Flex(2), "WINDOW USAGE · LAST 7 DAYS", "↺ resets · dashed: at this rate", func(c *ui.Ctx) { x.legend(c, legend, 0) },
		func(c *ui.Ctx) {
			widget.Chart(c, "c", series, widget.ChartOpts{Height: 170, Max: 100, Labels: labels, LabelEvery: 1, YTicks: 2,
				YFormat: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }, Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
				Hover: true, Highlight: -1, Marks: marks, Ticks: ticks, TicksLabel: "5h ↺", Title: func(i int) string {
					return start.Add(time.Duration(i) * time.Hour).In(x.loc).Format("Mon Jan 2 15:04")
				}, Label: "Window usage over the last 7 days", Help: "agent/usage#history"})
		})
}

// burnRow is a repo's part of a window: its sessions, the window's points it burned, and its cost.
type burnRow struct {
	key      string
	sessions int
	points   float64
	cost     float64
}

// burned spreads a window's points over the groups of r (hourly) by their share of each stretch's tokens: each rise
// between two readings goes to the hours it happened in, and a rise with no tokens behind it is outside.
func burned(r stats.Report, w core.UsageWindow, ss []reading) ([]burnRow, float64) {
	rows := make([]burnRow, len(r.Groups))
	for i, g := range r.Groups {
		rows[i] = burnRow{key: g.Key, sessions: g.Sessions, cost: g.Cost}
	}
	pts := slices.Clone(ss)
	if o := w.Opened(); !o.IsZero() {
		pts = append([]reading{{o, 0}}, pts...)
	}
	outside := 0.0
	for k := 1; k < len(pts); k++ {
		a, b := pts[k-1], pts[k]
		d := (b.used - a.used) * 100
		if d <= 0 {
			continue
		}
		per := make([]float64, len(rows))
		total := 0.0
		for i, p := range r.Points {
			end := p.Add(time.Hour)
			if !end.After(a.at) || !p.Before(b.at) {
				continue
			}
			for gi, g := range r.Groups {
				if i < len(g.Used) {
					per[gi] += float64(g.Used[i])
					total += float64(g.Used[i])
				}
			}
		}
		if total == 0 {
			outside += d
			continue
		}
		for gi := range rows {
			rows[gi].points += d * per[gi] / total
		}
	}
	return rows, outside
}

// burnedCard is what used one of the provider's windows, by repo.
func (x *statsExt) burnedCard(c *ui.Ctx, u core.ProviderUsage, hist []core.ProviderUsage, now time.Time) {
	th := c.Theme()
	if len(u.Windows) == 0 {
		return
	}
	pick, ok := x.burnWin[accountKey(u)]
	if !ok {
		pick = slices.IndexFunc(u.Windows, func(w core.UsageWindow) bool { return !fiveHour(w) && qualifier(w) == "" })
		pick = max(pick, 0)
	}
	pick = min(pick, len(u.Windows)-1)
	w := u.Windows[pick]
	from := w.Opened()
	if from.IsZero() {
		from = now.Add(-7 * 24 * time.Hour)
	}
	q := stats.Query{From: from.Truncate(time.Hour), To: now.Truncate(time.Hour).Add(time.Hour), Step: "hour", Offset: x.offset(),
		Filters: map[string][]string{"provider": {u.Provider}, "account": {u.Account}}, Split: x.split()}
	r, _ := x.report(q)
	rows, outside := burned(r, w, samples(w, hist))
	title := "BURNED THE " + strings.ToUpper(windowKind(w)) + " WINDOW"
	right := func(c *ui.Ctx) {
		if len(u.Windows) < 2 {
			return
		}
		var labels []string
		for _, w := range u.Windows {
			labels = append(labels, usageShort(w))
		}
		if widget.Segmented(c, "win", labels, &pick, widget.SegmentedOpts{Label: "Window", Help: "agent/usage#burned"}) {
			x.burnWin[accountKey(u)] = pick
		}
	}
	statsCard(c, "burned", ui.Flex(1), title, "", right, func(c *ui.Ctx) {
		note(c, "since", "since "+from.In(x.loc).Format("Mon 15:04")+" · by repo, multi-repo sessions split as in Stats")
		var strip []widget.ChartSeries
		st := x.h.State()
		for i, row := range rows {
			strip = append(strip, widget.ChartSeries{Name: row.key, Values: []float64{row.points}, Tone: repoTone(th, st, row.key, i)})
		}
		if outside > 0 {
			strip = append(strip, widget.ChartSeries{Name: "outside Oriel", Values: []float64{outside}, Tone: th.C(tStatsOther)})
		}
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Window points by repo", Help: "agent/usage#burned"})
		total := outside
		for i, row := range rows {
			total += row.points
			kv(c, i, repoTone(th, st, row.key, i), row.key, "", fmt.Sprintf("%d sess", row.sessions),
				fmt.Sprintf("%.0fpt", row.points), money(row.cost))
		}
		if outside > 0 {
			kv(c, "out", th.C(tStatsOther), "outside Oriel", "", "", fmt.Sprintf("%.0fpt", outside), "–")
		}
		kv(c, "total", render.Color{}, "total", "", "", fmt.Sprintf("%.0fpt", total), "")
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			note(c, "n", "pt: points of the window · $ est. from the price table")
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Link(c, "open", "Open in Stats ›", "agent/usage#burned") {
				x.run("stats.open", registry.Args{"provider": u.Provider, "account": u.Account})
			}
		})
	})
}

// usageAccount is the account the Usage page shows: the one picked, else the session in front's.
func (x *statsExt) usageAccount() (core.ProviderUsage, bool) {
	for _, u := range x.h.State().Usage {
		if x.acct != "" && accountKey(u) == x.acct {
			return u, true
		}
	}
	return x.frontUsage()
}

// accountSwitch picks the account the page shows. Accounts' windows are separate limits, so there's no "all".
func (x *statsExt) accountSwitch(c *ui.Ctx, cur core.ProviderUsage) {
	us := slices.Clone(x.h.State().Usage)
	if len(us) < 2 {
		return
	}
	slices.SortStableFunc(us, func(a, b core.ProviderUsage) int { return strings.Compare(a.Provider, b.Provider) })
	var labels []string
	at := 0
	for i, u := range us {
		labels = append(labels, strings.ToUpper(x.accountTitle(u)))
		if u.Same(cur) {
			at = i
		}
	}
	c.Row("accounts", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("k", "ACCOUNT", capText(c.Theme().C(theme.FgGhost)))
		if widget.Segmented(c, "acct", labels, &at, widget.SegmentedOpts{Label: "Account", Help: "agent/usage#accounts"}) {
			x.acct = accountKey(us[at])
		}
	})
}
