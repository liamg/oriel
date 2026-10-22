package stats

import (
	"fmt"
	"math"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// What the stats pages draw with: cards, tiles, tables, and the colours of token kinds.
var (
	tStatsUp      = theme.Declare("stats.delta.up", "accent")
	tStatsDown    = theme.Declare("stats.delta.down", "green")
	tStatsInput   = theme.Declare("stats.kind.input", "cyan")
	tStatsOutput  = theme.Declare("stats.kind.output", "amber")
	tStatsRead    = theme.DeclareOn("stats.kind.cache_read", "alpha(fg, 38%)", "bg.surface")
	tStatsWrite5m = theme.Declare("stats.kind.cache_write_5m", "lighten(violet, 12%)")
	tStatsWrite1h = theme.Declare("stats.kind.cache_write_1h", "violet")
	tStatsHours   = theme.Declare("stats.hours", "accent")
	tStatsOther   = theme.DeclareOn("stats.other", "alpha(fg, 30%)", "bg.surface")
	tStatsTrack   = theme.Declare("stats.track", "bg.overlay")
	tStatsBad     = theme.Declare("stats.bad", "red")
	tStatsGood    = theme.Declare("stats.good", "green")
	tStatsBarFill = theme.DeclareOn("stats.bar", "alpha(fg, 45%)", "bg.surface")
)

// greyTone is the ith of a strip of greys: models, efforts.
func greyTone(th *theme.Resolved, i int) render.Color {
	return theme.WithAlpha(th.C(theme.FgMuted), []float64{.75, .5, .32, .2}[min(i, 3)])
}

// kinds are the token kinds, in the order the charts stack them.
var kinds = []struct {
	name string
	tone theme.TokenID
	tok  func(stats.Tokens) int64
	cost func(stats.Costs) float64
}{
	{"cache read", tStatsRead, func(t stats.Tokens) int64 { return t.CacheRead }, func(c stats.Costs) float64 { return c.CacheRead }},
	{"cache write 1h", tStatsWrite1h, func(t stats.Tokens) int64 { return t.CacheWrite1h }, func(c stats.Costs) float64 { return c.CacheWrite1h }},
	{"cache write 5m", tStatsWrite5m, func(t stats.Tokens) int64 { return t.CacheWrite5m }, func(c stats.Costs) float64 { return c.CacheWrite5m }},
	{"output", tStatsOutput, func(t stats.Tokens) int64 { return t.Output }, func(c stats.Costs) float64 { return c.Output }},
	{"input", tStatsInput, func(t stats.Tokens) int64 { return t.Input }, func(c stats.Costs) float64 { return c.Input }},
}

// statsTile is a headline number: its label, value (with a smaller part after it) and a line under it.
func statsTile(c *ui.Ctx, key any, label, value, small string, sub func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 11, R: 11, T: 8, B: 9}, Gap: 6, Fill: th.C(kit.TStatsCard), Stroke: th.C(kit.TStatsCardLine),
		Border: 1, Radius: 8, Clip: true, Role: ui.RoleGroup, Label: label + " " + value}, func(c *ui.Ctx) {
		c.Text("l", label, kit.KickerFont(th.C(theme.FgFaint)))
		c.Row("v", ui.Style{Gap: 4, Cross: ui.End}, func(c *ui.Ctx) {
			c.Text("v", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 19, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if small != "" {
				c.Text("s", small, kit.MonoFont(11, th.C(theme.FgFaint)))
			}
		})
		c.Row("sub", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center, H: ui.Px(13)}, func(c *ui.Ctx) {
			if sub != nil {
				sub(c)
			}
		})
	})
}

// delta is a change against the period before, as a tile's sub line shows it: ▲ 12% in the up colour.
func delta(c *ui.Ctx, key any, cur, prev float64) {
	th := c.Theme()
	if prev <= 0 {
		return
	}
	d := (cur - prev) / prev * 100
	arrow, tone := "▲", th.C(tStatsUp)
	if d < 0 {
		arrow, tone, d = "▼", th.C(tStatsDown), -d
	}
	c.Text(key, fmt.Sprintf("%s %.0f%%", arrow, d), kit.MonoFont(10, tone))
}

func dim(c *ui.Ctx, key any, s string) { c.Text(key, s, kit.MonoFont(10, c.Theme().C(theme.FgFaint))) }

// shareBar is a share as a bar on a track, then its percentage.
func shareBar(c *ui.Ctx, key any, frac float64, tone render.Color, w float32, label string) {
	th := c.Theme()
	c.Row(key, ui.Style{Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
		f := float32(min(max(frac, 0), 1))
		c.Paint("bar", ui.Px(w), ui.Px(6), func(fr render.Frame, r render.Rect) {
			fr.Rect(r, render.Style{Fill: th.C(tStatsTrack), Radius: 2})
			if f > 0 {
				fr.Rect(render.XYWH(r.Min.X, r.Min.Y, max(r.Dx()*f, 2), r.Dy()), render.Style{Fill: tone, Radius: 2})
			}
		})
		if label != "" {
			c.Text("p", label, kit.MonoFont(11, th.C(theme.FgMuted)))
		}
	})
}

// hoursChart is cost by hour of the day, labelled every six hours.
func hoursChart(c *ui.Ctx, key any, hours [24]float64, height float32) {
	th := c.Theme()
	labels := make([]string, 24)
	for i := range labels {
		if i%6 == 0 || i == 23 {
			labels[i] = fmt.Sprintf("%02d", i)
		}
	}
	vals := hours[:]
	widget.Chart(c, key, []widget.ChartSeries{{Name: "cost", Values: vals, Tone: th.C(tStatsHours)}}, widget.ChartOpts{Height: height, Labels: labels,
		LabelEvery: 1, Format: kit.Money, Hover: true, Highlight: -1, Title: func(i int) string { return fmt.Sprintf("%02d:00", i) },
		Label: "Cost by hour of the day", Help: "agent/stats#chart"})
}

// dayLabels are a range's points as the charts label them: "Sep 15".
func dayLabels(points []time.Time) []string {
	out := make([]string, len(points))
	for i, p := range points {
		out[i] = p.Format("Jan 2")
	}
	return out
}

// hours is a duration as the tiles show it: 71h, 1h 48m.
func hours(secs int64) string {
	d := time.Duration(secs) * time.Second
	if d >= 10*time.Hour {
		return fmt.Sprintf("%dh", int(math.Round(d.Hours())))
	}
	return kit.Duration(d)
}

// axisMoney is a chart axis's dollars: $30, $0.50.
func axisMoney(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
