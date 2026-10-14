package app

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker/stats"
)

// What the stats pages draw with: cards, tiles, tables, and the colours of token kinds.
var (
	tStatsCard     = theme.Declare("stats.card.bg", "bg.surface")
	tStatsCardLine = theme.Declare("stats.card.line", "line")
	tStatsHead     = theme.Declare("stats.header.bg", "bg.surface")
	tStatsRowOn    = theme.DeclareOn("stats.row.selected", "alpha(accent, 8%)", "bg.surface")
	tStatsUp       = theme.Declare("stats.delta.up", "accent")
	tStatsDown     = theme.Declare("stats.delta.down", "green")
	tStatsInput    = theme.Declare("stats.kind.input", "cyan")
	tStatsOutput   = theme.Declare("stats.kind.output", "amber")
	tStatsRead     = theme.DeclareOn("stats.kind.cache_read", "alpha(fg, 38%)", "bg.surface")
	tStatsWrite5m  = theme.Declare("stats.kind.cache_write_5m", "lighten(violet, 12%)")
	tStatsWrite1h  = theme.Declare("stats.kind.cache_write_1h", "violet")
	tStatsHours    = theme.Declare("stats.hours", "accent")
	tStatsOther    = theme.DeclareOn("stats.other", "alpha(fg, 30%)", "bg.surface")
	tStatsTrack    = theme.Declare("stats.track", "bg.overlay")
	tStatsBad      = theme.Declare("stats.bad", "red")
	tStatsGood     = theme.Declare("stats.good", "green")
	tStatsBarFill  = theme.DeclareOn("stats.bar", "alpha(fg, 45%)", "bg.surface")
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

func capText(col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14}, Color: col}
}

// statsCard is a card with a kicker, a dim note after it, what's at its right, and its body.
func statsCard(c *ui.Ctx, key any, w ui.Size, title, note string, right func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: w, Pad: ui.Insets{L: 12, R: 12, T: 9, B: 10}, Gap: 8, Fill: th.C(tStatsCard), Stroke: th.C(tStatsCardLine), Border: 1,
		Radius: 8, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("hd", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", title, capText(th.C(theme.FgMuted)))
			if note != "" {
				c.Text("n", note, monoFont(10, th.C(theme.FgGhost)))
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if right != nil {
				right(c)
			}
		})
		body(c)
	})
}

// statsTile is a headline number: its label, value (with a smaller part after it) and a line under it.
func statsTile(c *ui.Ctx, key any, label, value, small string, sub func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 11, R: 11, T: 8, B: 9}, Gap: 6, Fill: th.C(tStatsCard), Stroke: th.C(tStatsCardLine),
		Border: 1, Radius: 8, Clip: true, Role: ui.RoleGroup, Label: label + " " + value}, func(c *ui.Ctx) {
		c.Text("l", label, capText(th.C(theme.FgFaint)))
		c.Row("v", ui.Style{Gap: 4, Cross: ui.End}, func(c *ui.Ctx) {
			c.Text("v", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 19, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if small != "" {
				c.Text("s", small, monoFont(11, th.C(theme.FgFaint)))
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
	c.Text(key, fmt.Sprintf("%s %.0f%%", arrow, d), monoFont(10, tone))
}

func dim(c *ui.Ctx, key any, s string) { c.Text(key, s, monoFont(10, c.Theme().C(theme.FgFaint))) }

// statsCol is a table column: its title and width, right-aligned unless it's the first.
type statsCol struct {
	title string
	w     float32 // 0 takes what's left
}

func colSize(w float32) ui.Size {
	if w == 0 {
		return ui.Flex(1)
	}
	return ui.Px(w)
}

// tableHead is a table's header row.
func tableHead(c *ui.Ctx, cols []statsCol) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("cols", ui.Style{W: ui.Flex(1), H: ui.Px(18), Cross: ui.Center}, func(c *ui.Ctx) {
			for i, col := range cols {
				main := ui.End
				if i == 0 {
					main = ui.Start
				}
				c.Row(i, ui.Style{W: colSize(col.w), Main: main, Pad: ui.PadXY(6, 0), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", col.title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .1},
						Color: th.C(theme.FgGhost)})
				})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	})
}

// cell is a table cell of width w: right-aligned text, or what body draws.
func cell(c *ui.Ctx, key any, w float32, first bool, body func(c *ui.Ctx)) {
	main := ui.End
	if first {
		main = ui.Start
	}
	c.Row(key, ui.Style{W: colSize(w), Main: main, Cross: ui.Center, Gap: 7, Pad: ui.PadXY(6, 0), Clip: true}, body)
}

func textCell(c *ui.Ctx, key any, w float32, s string, tone render.Color) {
	cell(c, key, w, false, func(c *ui.Ctx) { c.Text("t", s, monoFont(11, tone)) })
}

// swatch is a series' small square.
func swatch(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: tone}, nil)
}

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
			c.Text("p", label, monoFont(11, th.C(theme.FgMuted)))
		}
	})
}

// kv is a row of a small card: a swatch and a name, then values at the right.
func kv(c *ui.Ctx, key any, tone render.Color, name, note string, values ...string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: name}, func(c *ui.Ctx) {
		if tone.A > 0 {
			swatch(c, "sw", tone)
		}
		c.Text("n", name, monoFont(11, th.C(theme.Fg)))
		if note != "" {
			c.Text("note", note, monoFont(10, th.C(theme.FgGhost)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		for i, v := range values {
			w := float32(44)
			if i == len(values)-1 {
				w = 58
			}
			c.Row(i, ui.Style{W: ui.Px(w), Main: ui.End}, func(c *ui.Ctx) {
				tone := th.C(theme.FgMuted)
				if i == len(values)-1 {
					tone = th.C(theme.Fg)
				}
				c.Text("v", v, monoFont(11, tone))
			})
		}
	})
}

func note(c *ui.Ctx, key any, s string) {
	c.Text(key, s, monoFont(10, c.Theme().C(theme.FgGhost)))
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
		LabelEvery: 1, Format: money, Hover: true, Highlight: -1, Title: func(i int) string { return fmt.Sprintf("%02d:00", i) },
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

// repoTones are repos' colours, each its own: a repo of the session in front keeps its project colour there (the
// sidebar's), and the others take the palette's colours it doesn't use, in order.
func repoTones(th *theme.Resolved, st core.State, names []string) []render.Color {
	out := make([]render.Color, len(names))
	used := map[string]bool{}
	got := make([]string, len(names))
	if se, ok := st.Session(st.Focus); ok {
		s := se.Snapshot()
		for i, n := range names {
			if hasProject(s, n) {
				got[i] = s.ProjectColour(n)
				used[got[i]] = true
			}
		}
	}
	next := 0
	for i := range names {
		for got[i] == "" && next < len(core.ProjectColours) {
			if c := core.ProjectColours[next]; !used[c] {
				got[i], used[c] = c, true
			}
			next++
		}
		out[i] = tokenColour(th, got[i], paletteTone(th, i))
	}
	return out
}

// repoTone is a repo's colour, the same wherever the page draws it: among the session in front's repos and then
// every other repo the app knows, by name.
func repoTone(th *theme.Resolved, st core.State, name string, _ int) render.Color {
	var names, rest []string
	if se, ok := st.Session(st.Focus); ok {
		for _, p := range se.Snapshot().Projects {
			names = append(names, p.Name)
		}
	}
	for _, se := range st.Sessions {
		for _, p := range se.Projects {
			if !slices.Contains(names, p.Name) && !slices.Contains(rest, p.Name) {
				rest = append(rest, p.Name)
			}
		}
	}
	slices.Sort(rest)
	if !slices.Contains(rest, name) && !slices.Contains(names, name) {
		rest = append(rest, name)
	}
	all := append(names, rest...)
	return repoTones(th, st, all)[slices.Index(all, name)]
}

func hasProject(se core.SessionState, name string) bool {
	for _, p := range se.Projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

func paletteTone(th *theme.Resolved, rank int) render.Color {
	return tokenColour(th, core.ProjectColours[rank%len(core.ProjectColours)], th.C(theme.Accent))
}

// hours is a duration as the tiles show it: 71h, 1h 48m.
func hours(secs int64) string {
	d := time.Duration(secs) * time.Second
	if d >= 10*time.Hour {
		return fmt.Sprintf("%dh", int(math.Round(d.Hours())))
	}
	return hm(d)
}

// axisMoney is a chart axis's dollars: $30, $0.50.
func axisMoney(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
