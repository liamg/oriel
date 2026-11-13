package stats

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The global Stats page: every session, grouped by repo by default, over the
// main area beside the sidebar.

// statsTop is how many groups the chart draws apart; the rest are one "other" series.
const statsTop = 6

func (x *Plugin) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	q := x.query()
	r, meta := x.report(q)
	if q.Group == stats.ByAccount {
		for i, g := range r.Groups {
			r.Groups[i].Label = x.accountLabel(g.Key, g.Label)
		}
	}
	if x.sel >= len(r.Groups) {
		x.sel = max(len(r.Groups)-1, 0)
	}
	x.hovering = -1
	defer func() { x.hover = x.hovering }()
	widget.Page(c, "stats", &x.open, widget.PageOpts{KeyMode: StatsKeyMode, Label: "Stats", Help: "agent/stats#page"},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.header(c, q, r, meta)
			x.toolbar(c)
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
				Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
				widget.Async(c, "load", meta.state, widget.AsyncOpts{Label: "Scanning the sessions", Retry: func() { x.retryReport(q) },
					Loading: statsSkeleton, Help: "agent/stats#page"}, func(c *ui.Ctx) { x.reportBody(c, q, r) })
			})
		})
}

// statsSkeleton stands in for the tiles and charts until the first answer.
func statsSkeleton(c *ui.Ctx) {
	const help = "agent/stats#page"
	c.Column("skeleton", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		widget.Skeleton(c, "tiles", widget.SkeletonOpts{Block: render.Point{X: 180, Y: 64}, Count: 5, Dir: ui.Row, Gap: 12, Label: "Stats", Help: help})
		widget.Skeleton(c, "chart", widget.SkeletonOpts{Block: render.Point{X: 760, Y: 220}, Label: "Chart", Help: help})
		widget.Skeleton(c, "rows", widget.SkeletonOpts{Rows: 6, RowH: 14, Widths: []float32{1, .9, .75}, Label: "Table", Help: help})
	})
}

// reportBody is the page's loaded body: the tiles, the chart and table, and the small cards.
func (x *Plugin) reportBody(c *ui.Ctx, q stats.Query, r stats.Report) {
	c.Column("report", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		x.tiles(c, q, r)
		tones := x.groupTones(c, r)
		if q.Group == stats.BySession {
			c.Row("pair", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
				x.chart(c, q, r, tones, ui.Flex(1))
				x.table(c, q, r, tones, ui.Flex(1))
			})
		} else {
			x.chart(c, q, r, tones, ui.Flex(1))
			x.table(c, q, r, tones, ui.Flex(1))
		}
		c.Row("small", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			tokensByKind(c, r.Tokens, r.Costs)
			modelsCard(c, r.Models)
			toolsByErrors(c, r.Tools)
			kit.StatsCard(c, "hours", ui.Flex(1), "HOURS OF THE DAY", "", func(c *ui.Ctx) { kit.Note(c, "n", "cost by hour · local") }, func(c *ui.Ctx) {
				hoursChart(c, "chart", r.Hours, 88)
			})
		})
	})
}

func (x *Plugin) header(c *ui.Ctx, q stats.Query, r stats.Report, meta reportMeta) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "STATS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		scope := "all sessions"
		if len(q.Filters) > 0 {
			var parts []string
			for _, k := range []string{"repo", "session", "kind", "model", "provider", "account", "origin"} {
				if v := q.Filters[k]; len(v) > 0 {
					parts = append(parts, k+" "+strings.Join(v, ", "))
				}
			}
			if len(parts) > 0 {
				scope = strings.Join(parts, " · ")
			}
		}
		meta2 := []string{scope, kit.Counted(r.Totals.Sessions, "session"), q.From.Format("Jan 2") + " – " + q.To.AddDate(0, 0, -1).Format("Jan 2")}
		c.Text("m", strings.Join(meta2, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		var origins []string
		o := 0
		for i, v := range statsOrigins {
			origins = append(origins, v.label)
			if f := x.q.filters["origin"]; len(f) == 1 && f[0] == v.value {
				o = i
			}
		}
		if widget.Segmented(c, "origin", origins, &o, widget.SegmentedOpts{Label: "Origin", Help: "agent/stats#origin"}) {
			x.run("stats.origin", registry.Args{"origin": statsOrigins[o].value})
		}
		switch {
		case meta.state.Phase == widget.Pending:
			widget.Busy(c, "scanned", meta.state, widget.BusyOpts{Label: "Scanning", Size: 11, Help: "agent/stats#page"})
		case !meta.at.IsZero():
			widget.Ago(c, "scanned", meta.at, widget.AgoOpts{Text: kit.MonoFont(11, th.C(theme.FgFaint)), Long: true, Prefix: "scanned ", Now: x.now()})
		}
		if one := q.Filters["session"]; len(one) == 1 && widget.Button(c, "tab", "Session stats", widget.ButtonOpts{Help: "agent/stats#session"}) {
			x.run("stats.session", registry.Args{"id": one[0]})
		}
		if widget.Button(c, "csv", "Copy as CSV", widget.ButtonOpts{Help: "agent/stats#table"}) {
			x.run("stats.copy", nil)
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "agent/stats#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func (x *Plugin) run(id string, a registry.Args) {
	kit.RunOrNotify(x.h, "stats", id, a)
}

// toolbar is the range, the grouping, the filter chips and the split toggle.
func (x *Plugin) toolbar(c *ui.Ctx) {
	th := c.Theme()
	c.Row("bar", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		var labels []string
		for _, r := range statsRanges {
			labels = append(labels, r.label)
		}
		rng := x.q.rng
		anchor := c.Next("range")
		if widget.Segmented(c, "range", labels, &rng, widget.SegmentedOpts{Label: "Range", Help: "agent/stats#range"}) {
			if statsRanges[rng].days == 0 {
				x.picking = true
			} else {
				x.q.rng = rng
			}
		}
		if statsRanges[x.q.rng].days == 0 && widget.IconButton(c, "custom", "▾", widget.IconButtonOpts{Label: "Change the custom range",
			Help: "agent/stats#range"}) {
			x.picking = true
		}
		from, to := x.span(x.q)
		pick := widget.DateRange{From: from, To: to.AddDate(0, 0, -1)}
		if widget.DatePopover(c, "pick", anchor, &x.picking, &pick, widget.DatePickerOpts{Label: "Custom range", Help: "agent/stats#range",
			Today: x.now(), Location: x.Loc}) {
			x.custom(pick)
		}
		c.Text("by", "GROUP BY", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .12},
			Color: th.C(theme.FgGhost)})
		// Origin is a grouping by key (g o) and shown only while it's the one: the bar has no room for it otherwise.
		var groups []string
		var keys []string
		g := 0
		for _, s := range statsGroups {
			if s.key == stats.ByOrigin && x.q.group != stats.ByOrigin {
				continue
			}
			if s.key == x.q.group {
				g = len(groups)
			}
			groups, keys = append(groups, s.label), append(keys, s.key)
		}
		if widget.Segmented(c, "group", groups, &g, widget.SegmentedOpts{Label: "Group by", Help: "agent/stats#group-by"}) {
			x.q.group, x.sel = keys[g], 0
		}
		for _, k := range []string{"repo", "provider", "account", "session", "kind", "model"} {
			x.filterChips(c, k)
		}
		if opts := x.moreFilters(); len(opts) > 0 {
			if v, ok := widget.Select(c, "more", "+ filter", opts, widget.SelectOpts{Label: "Add a filter", Help: "agent/stats#filters"}); ok {
				k, val, _ := strings.Cut(v, " ")
				x.q.filters[k] = append(x.q.filters[k], x.filterValue(k, val))
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		on := x.split()
		label := "split multi-repo sessions"
		if x.q.group == stats.BySession {
			label += " · n/a by session"
		}
		if widget.Toggle(c, "split", on, widget.ToggleOpts{Label: "Split multi-repo sessions", Help: "agent/stats#split"}) {
			x.run("stats.split", registry.Args{"on": !on})
		}
		c.Text("splitl", label, kit.UIFont(11.5, th.C(theme.FgMuted)))
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// filterChips are a filter's chips: one for each value, ✕ taking it off; for the main three with none, a menu of
// what there is.
func (x *Plugin) filterChips(c *ui.Ctx, key string) {
	th := c.Theme()
	vals := x.q.filters[key]
	for i, v := range vals {
		label := v
		if key == "account" {
			label = x.accountLabel(v, "")
		}
		if widget.Clickable(c, key+fmt.Sprint(i), ui.Style{Dir: ui.Row, H: ui.Px(22), Pad: ui.PadXY(8, 0), Gap: 5, Cross: ui.Center, Radius: 11,
			Fill: th.C(theme.AccentBg), Stroke: theme.WithAlpha(th.C(theme.Accent), .35), Border: 1, Label: "Remove " + key + " " + label,
			Help: "agent/stats#filters"}, func(c *ui.Ctx) {
			c.Text("k", key, kit.MonoFont(10.5, th.C(theme.FgGhost)))
			c.Text("v", label, kit.MonoFont(10.5, th.C(theme.Fg)))
			c.Text("x", "✕", kit.MonoFont(9, th.C(theme.FgGhost)))
		}).Clicked {
			x.q.filters[key] = slices.Delete(slices.Clone(vals), i, i+1)
			if len(x.q.filters[key]) == 0 {
				delete(x.q.filters, key)
			}
			return
		}
	}
	if len(vals) > 0 || key != "repo" && key != "provider" && key != "account" {
		return
	}
	opts := x.values(key)
	if v, ok := widget.Select(c, "f."+key, key+" all", opts, widget.SelectOpts{Label: "Filter by " + key, Help: "agent/stats#filters"}); ok {
		x.q.filters[key] = []string{x.filterValue(key, v)}
	}
}

// values are what a filter can be: the repos and providers the app knows of; sessions, kinds and models.
func (x *Plugin) values(key string) []string {
	st := x.h.State()
	var out []string
	add := func(v string) {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	switch key {
	case "account":
		for _, u := range st.Usage {
			add(x.accountLabel(u.Account, u.AccountName))
		}
	case "model":
		r, _ := x.report(x.query())
		for _, m := range r.Models {
			add(m.Name)
		}
	default:
		for _, se := range st.Sessions {
			switch key {
			case "repo":
				for _, p := range se.Projects {
					add(p.Name)
				}
			case "provider":
				add(se.Agent.Provider)
			case "kind":
				add(string(se.Kind))
			case "session":
				add(kit.Label(se))
			}
		}
	}
	slices.Sort(out)
	return out
}

// filterValue is what a filter's menu shows as what the query wants: an account's or session's ID for its name.
func (x *Plugin) filterValue(key, v string) string {
	st := x.h.State()
	switch key {
	case "account":
		for _, u := range st.Usage {
			if x.accountLabel(u.Account, u.AccountName) == v {
				return u.Account
			}
		}
	case "session":
		for _, se := range st.Sessions {
			if se.Name == v {
				return string(se.ID)
			}
		}
	}
	return v
}

// moreFilters are the filters + filter offers: a session, kind or model.
func (x *Plugin) moreFilters() []string {
	var out []string
	for _, k := range []string{"session", "kind", "model"} {
		for _, v := range x.values(k) {
			if !slices.Contains(x.q.filters[k], x.filterValue(k, v)) {
				out = append(out, k+" "+v)
			}
		}
	}
	return out
}

func (x *Plugin) tiles(c *ui.Ctx, q stats.Query, r stats.Report) {
	t, p := r.Totals, r.Prev
	days := int(math.Round(q.To.Sub(q.From).Hours() / 24))
	c.Row("tiles", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		costTile(c, kit.Money(t.Cost), r.Prices, func(c *ui.Ctx) {
			delta(c, "d", t.Cost, p.Cost)
			if p.Cost > 0 {
				dim(c, "s", fmt.Sprintf("vs prev %dd %s", days, kit.Money(p.Cost)))
			}
		})
		all := t.Tokens.Total()
		statsTile(c, "tokens", "TOKENS", kit.Count(all), "", func(c *ui.Ctx) {
			delta(c, "d", float64(all), float64(p.Tokens.Total()))
			dim(c, "s", fmt.Sprintf("cache hit %d%%", kit.Pct(cacheHit(t.Tokens))))
			if out := outsideTokens(r); out > 0 && out < all {
				c.Text("o", fmt.Sprintf("%d%% outside", kit.Pct(float64(out)/float64(all))), kit.MonoFont(10, c.Theme().C(theme.FgMuted)))
			}
		})
		statsTile(c, "requests", "REQUESTS", kit.Count(t.Requests), "", func(c *ui.Ctx) {
			delta(c, "d", float64(t.Requests), float64(p.Requests))
			dim(c, "s", kit.Counted(int(t.Turns), "turn"))
		})
		statsTile(c, "sessions", "SESSIONS", fmt.Sprint(t.Sessions), "", func(c *ui.Ctx) {
			delta(c, "d", float64(t.Sessions), float64(p.Sessions))
			dim(c, "s", fmt.Sprintf("%d multi-repo", t.MultiRepo))
		})
		statsTile(c, "tools", "TOOL CALLS", kit.Count(t.ToolCalls), "", func(c *ui.Ctx) {
			rate, prev := errRate(t), errRate(p)
			if p.ToolCalls > 0 {
				d := (rate - prev) * 100
				arrow, tone := "▲", c.Theme().C(tStatsUp)
				if d < 0 {
					arrow, tone, d = "▼", c.Theme().C(tStatsDown), -d
				}
				c.Text("d", fmt.Sprintf("%s %.1fpt", arrow, d), kit.MonoFont(10, tone))
			}
			dim(c, "s", fmt.Sprintf("%.1f%% errored", rate*100))
		})
		span := ""
		if t.SpanS > 0 {
			span = "of " + hours(t.SpanS)
		}
		statsTile(c, "active", "ACTIVE", hours(t.ActiveS), span, func(c *ui.Ctx) {
			delta(c, "d", float64(t.ActiveS), float64(p.ActiveS))
			if t.ActiveS > 0 {
				dim(c, "s", kit.Money(t.Cost/(float64(t.ActiveS)/3600))+" / active hour")
			}
		})
	})
}

// outsideTokens are the tokens of a report's sessions outside Oriel.
func outsideTokens(r stats.Report) int64 {
	for _, o := range r.Origins {
		if o.Name == stats.OriginOutside {
			return o.Tokens.Total()
		}
	}
	return 0
}

func errRate(t stats.Totals) float64 {
	if t.ToolCalls == 0 {
		return 0
	}
	return float64(t.ToolErrors) / float64(t.ToolCalls)
}

func cacheHit(t stats.Tokens) float64 {
	if in := t.Prompt(); in > 0 {
		return float64(t.CacheRead) / float64(in)
	}
	return 0
}

// groupTones are each group's colour: a repo's project colour, else the palette's by rank.
func (x *Plugin) groupTones(c *ui.Ctx, r stats.Report) []render.Color {
	th := c.Theme()
	st := x.h.State()
	out := make([]render.Color, len(r.Groups))
	if x.q.group == stats.ByOrigin {
		for i, g := range r.Groups {
			out[i] = th.C(theme.Accent)
			if g.Key == stats.OriginOutside {
				out[i] = th.C(tStatsOther)
			}
		}
		return out
	}
	if x.q.group != stats.ByRepo {
		for i := range r.Groups {
			out[i] = kit.PaletteTone(th, i)
		}
		return out
	}
	for i, g := range r.Groups {
		out[i] = kit.RepoTone(th, st, g.Key, i)
	}
	return out
}

// series are the chart's: the top groups, then the rest as one.
func (x *Plugin) series(c *ui.Ctx, r stats.Report, tones []render.Color) []widget.ChartSeries {
	var out []widget.ChartSeries
	other := make([]float64, len(r.Points))
	rest := 0
	for i, g := range r.Groups {
		if i < statsTop {
			out = append(out, widget.ChartSeries{Name: g.Label, Values: g.Series, Tone: tones[i]})
			continue
		}
		rest++
		for j, v := range g.Series {
			if j < len(other) {
				other[j] += v
			}
		}
	}
	if rest > 0 {
		out = append(out, widget.ChartSeries{Name: fmt.Sprintf("other · %d", rest), Values: other, Tone: c.Theme().C(tStatsOther)})
	}
	return out
}

func (x *Plugin) chart(c *ui.Ctx, q stats.Query, r stats.Report, tones []render.Color, w ui.Size) {
	series := x.series(c, r, tones)
	what := map[string]string{stats.ByRepo: "REPO", stats.BySession: "SESSION", stats.ByKind: "KIND", stats.ByModel: "MODEL",
		stats.ByProvider: "PROVIDER", stats.ByAccount: "ACCOUNT", stats.ByOrigin: "ORIGIN", stats.ByDay: "DAY"}[q.Group]
	sub := "stacked · split sessions"
	if !q.Split {
		sub = "stacked · whole sessions"
	}
	if q.Group == stats.BySession {
		sub = fmt.Sprintf("top %d · the rest as other", statsTop)
	}
	hl := -1
	if x.hover >= 0 && x.hover < len(series) {
		hl = x.hover
	}
	kit.StatsCard(c, "chart", w, "COST PER "+what+" PER DAY", sub, func(c *ui.Ctx) {
		if q.Group == stats.BySession {
			return
		}
		x.legend(c, series, 0)
	}, func(c *ui.Ctx) {
		if q.Group == stats.BySession {
			c.Row("legend", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.legend(c, series, 4) })
		}
		// The hovered series' line keeps its place empty, so hovering never moves the chart under the pointer.
		c.Row("pick", ui.Style{W: ui.Flex(1), Main: ui.End, H: ui.Px(12)}, func(c *ui.Ctx) {
			if hl >= 0 && hl < len(r.Groups) && r.Totals.Cost > 0 {
				g := r.Groups[hl]
				c.Text("t", fmt.Sprintf("%s · %s · %.1f%%", g.Label, kit.Money(g.Cost), g.Cost/r.Totals.Cost*100), kit.MonoFont(10, tones[hl]))
			}
		})
		res := widget.Chart(c, "c", series, widget.ChartOpts{Height: 140, Labels: dayLabels(r.Points), YTicks: 3, YFormat: axisMoney, Format: kit.Money, Hover: true, Highlight: hl, Title: func(i int) string {
			if i < len(r.Points) {
				return r.Points[i].Format("Mon Jan 2")
			}
			return ""
		}, Label: "Cost per " + q.Group + " per day", Help: "agent/stats#chart"})
		if res.Series >= 0 {
			x.hovering = res.Series
		}
		if res.Clicked && res.Series >= 0 && res.Series < min(len(r.Groups), statsTop) {
			x.drill(r.Groups[res.Series])
		}
	})
}

// legend is the series' names in their colours, perLine to a line (0 for one line).
func (x *Plugin) legend(c *ui.Ctx, series []widget.ChartSeries, perLine int) {
	th := c.Theme()
	if perLine <= 0 {
		perLine = len(series)
	}
	c.Column("legend", ui.Style{Gap: 6}, func(c *ui.Ctx) {
		for l := 0; l*perLine < len(series); l++ {
			c.Row(l, ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
				for i, s := range series[l*perLine : min(len(series), (l+1)*perLine)] {
					c.Row(i, ui.Style{Gap: 5, Cross: ui.Center}, func(c *ui.Ctx) {
						kit.Swatch(c, "sw", s.Tone)
						c.Text("n", s.Name, kit.MonoFont(10.5, th.C(theme.FgMuted)))
					})
				}
			})
		}
	})
}

// table is the groups: a row each, the selected one drilled into with ⏎ or a click.
func (x *Plugin) table(c *ui.Ctx, q stats.Query, r stats.Report, tones []render.Color, w ui.Size) {
	th := c.Theme()
	what := map[string]string{stats.ByRepo: "repo", stats.BySession: "session", stats.ByKind: "kind", stats.ByModel: "model",
		stats.ByProvider: "provider", stats.ByAccount: "account", stats.ByOrigin: "origin", stats.ByDay: "day"}[q.Group]
	right := func(c *ui.Ctx) {
		if x.sel < len(r.Groups) {
			c.Row("hint", ui.Style{Gap: 5, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("k", ui.Style{Pad: ui.PadXY(4, 0), Radius: 3, Stroke: theme.WithAlpha(th.C(theme.Accent), .4), Border: 1}, func(c *ui.Ctx) {
					c.Text("t", "⏎", kit.MonoFont(9, th.C(theme.Accent)))
				})
				c.Text("t", "drill into "+r.Groups[x.sel].Label, kit.MonoFont(10, th.C(theme.Accent)))
			})
		}
	}
	sorted := "sorted by cost"
	if q.Group == stats.ByDay {
		sorted = "by date"
	}
	kit.StatsCard(c, "table", w, strings.ToUpper(what)+"S", fmt.Sprintf("%s · %s", kit.Counted(len(r.Groups), what), sorted), right, func(c *ui.Ctx) {
		cols := []kit.StatsCol{{Title: strings.ToUpper(what), W: 0}, {Title: "SESSIONS", W: 70}, {Title: "REQUESTS", W: 80}, {Title: "TOKENS", W: 70}, {Title: "COST", W: 80}, {Title: "SHARE", W: 150},
			{Title: "LAST ACTIVE", W: 90}, {Title: "30 DAYS", W: 130}, {Title: "", W: 20}}
		if q.Group == stats.BySession {
			cols = []kit.StatsCol{{Title: "SESSION", W: 0}, {Title: "REPOS", W: 200}, {Title: "DAYS", W: 50}, {Title: "REQUESTS", W: 80}, {Title: "COST", W: 80}, {Title: "LAST ACTIVE", W: 90}}
		}
		kit.TableHead(c, cols)
		shown := r.Groups
		const most = 12
		if len(shown) > most {
			shown = shown[:most-1]
		}
		for i, g := range shown {
			x.row(c, i, g, r, cols, tones[i], q.Group == stats.BySession)
		}
		if rest := r.Groups[len(shown):]; len(rest) > 0 {
			var req int64
			var cost float64
			var repos []string
			for _, g := range rest {
				req += g.Requests
				cost += g.Cost
				for _, rp := range g.Repos {
					if !slices.Contains(repos, rp) {
						repos = append(repos, rp)
					}
				}
			}
			c.Row("more", ui.Style{W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center}, func(c *ui.Ctx) {
				kit.Cell(c, 0, cols[0].W, true, func(c *ui.Ctx) { c.Text("t", fmt.Sprintf("%d more", len(rest)), kit.MonoFont(11, th.C(theme.FgFaint))) })
				if q.Group == stats.BySession {
					kit.TextCell(c, 1, cols[1].W, strings.Join(repos, ", "), th.C(theme.FgGhost))
					kit.TextCell(c, 2, cols[2].W, "", th.C(theme.FgGhost))
					kit.TextCell(c, 3, cols[3].W, kit.Count(req), th.C(theme.FgMuted))
					kit.TextCell(c, 4, cols[4].W, kit.Money(cost), th.C(theme.FgMuted))
					kit.TextCell(c, 5, cols[5].W, "", th.C(theme.FgGhost))
				}
			})
		}
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.LineStrong)}, nil)
		t := r.Totals
		c.Row("total", ui.Style{W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center, Role: ui.RoleText, Label: "total"}, func(c *ui.Ctx) {
			fg := th.C(theme.Fg)
			kit.Cell(c, 0, cols[0].W, true, func(c *ui.Ctx) { c.Text("t", "total", kit.MonoFont(11, fg)) })
			if q.Group == stats.BySession {
				kit.TextCell(c, 1, cols[1].W, fmt.Sprintf("%d of %d span several repos", t.MultiRepo, t.Sessions), th.C(theme.FgGhost))
				kit.TextCell(c, 2, cols[2].W, "", fg)
				kit.TextCell(c, 3, cols[3].W, kit.Count(t.Requests), fg)
				kit.TextCell(c, 4, cols[4].W, kit.Money(t.Cost), fg)
				kit.TextCell(c, 5, cols[5].W, "", fg)
				return
			}
			kit.TextCell(c, 1, cols[1].W, fmt.Sprint(t.Sessions), fg)
			kit.TextCell(c, 2, cols[2].W, kit.Count(t.Requests), fg)
			kit.TextCell(c, 3, cols[3].W, kit.Count(t.Tokens.Total()), fg)
			kit.TextCell(c, 4, cols[4].W, kit.Money(t.Cost), fg)
			kit.Cell(c, 5, 0, true, func(c *ui.Ctx) {
				if q.Group == stats.ByRepo && q.Split {
					c.Text("t", fmt.Sprintf("%d repo-sessions from %d", t.Pairs, t.Sessions), kit.MonoFont(10.5, th.C(theme.FgGhost)))
				}
			})
		})
	})
}

func (x *Plugin) row(c *ui.Ctx, i int, g stats.Group, r stats.Report, cols []kit.StatsCol, tone render.Color, bySession bool) {
	th := c.Theme()
	on := i == x.sel || i == x.hover
	fill := render.Color{}
	if on {
		fill = th.C(kit.TStatsRowOn)
	}
	in := widget.Clickable(c, "row"+g.Key, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center, Fill: fill, NoHover: true,
		Role: ui.RoleListItem, Label: g.Label, Selected: i == x.sel, Help: "agent/stats#table"}, func(c *ui.Ctx) {
		edge := render.Color{}
		if i == x.sel {
			edge = th.C(theme.Accent)
		}
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		fg, muted := th.C(theme.Fg), th.C(theme.FgMuted)
		kit.Cell(c, 0, cols[0].W, true, func(c *ui.Ctx) {
			kit.Swatch(c, "sw", tone)
			c.Text("t", g.Label, kit.Mono(11.5, typeset.Medium, fg))
			if g.Origin == stats.OriginOutside {
				c.Text("o", "outside", kit.MonoFont(10, th.C(theme.FgGhost)))
			}
		})
		if bySession {
			kit.Cell(c, 1, cols[1].W, true, func(c *ui.Ctx) {
				for j, rp := range g.Repos {
					c.Row(j, ui.Style{Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
						kit.Swatch(c, "sw", kit.RepoTone(th, x.h.State(), rp, j))
						c.Text("t", rp, kit.MonoFont(11, muted))
					})
				}
			})
			days := 0
			for _, v := range g.Series {
				if v > 0 {
					days++
				}
			}
			kit.TextCell(c, 2, cols[2].W, fmt.Sprint(days), muted)
			kit.TextCell(c, 3, cols[3].W, kit.Count(g.Requests), muted)
			kit.TextCell(c, 4, cols[4].W, kit.Money(g.Cost), fg)
			kit.Cell(c, 5, cols[5].W, false, func(c *ui.Ctx) {
				widget.Ago(c, "t", g.Last, widget.AgoOpts{Text: kit.MonoFont(11, muted), Long: true, Now: x.now()})
			})
			return
		}
		kit.TextCell(c, 1, cols[1].W, fmt.Sprint(g.Sessions), muted)
		kit.TextCell(c, 2, cols[2].W, kit.Count(g.Requests), muted)
		kit.TextCell(c, 3, cols[3].W, kit.Count(g.Tokens), muted)
		kit.TextCell(c, 4, cols[4].W, kit.Money(g.Cost), fg)
		share := 0.0
		if r.Totals.Cost > 0 {
			share = g.Cost / r.Totals.Cost
		}
		kit.Cell(c, 5, cols[5].W, true, func(c *ui.Ctx) { shareBar(c, "share", share, tone, 90, fmt.Sprintf("%.1f%%", share*100)) })
		kit.Cell(c, 6, cols[6].W, false, func(c *ui.Ctx) {
			widget.Ago(c, "t", g.Last, widget.AgoOpts{Text: kit.MonoFont(11, muted), Long: true, Now: x.now()})
		})
		kit.Cell(c, 7, cols[7].W, false, func(c *ui.Ctx) {
			widget.Chart(c, "spark", []widget.ChartSeries{{Name: g.Label, Values: g.Series, Tone: tone}}, widget.ChartOpts{Kind: widget.ChartSpark,
				Width: ui.Px(120), Highlight: -1, Label: g.Label + " by day", Help: "agent/stats#table"})
		})
		kit.TextCell(c, 8, cols[8].W, "›", th.C(theme.FgGhost))
	})
	if in.Hovered || in.Inside {
		x.hovering = i
	}
	if in.Clicked {
		x.sel = i
		x.drill(g)
	}
}

// tokensByKind is the tokens and their cost by kind, with a strip of the cost.
func tokensByKind(c *ui.Ctx, t stats.Tokens, cost stats.Costs) {
	th := c.Theme()
	kit.StatsCard(c, "kinds", ui.Flex(1), "TOKENS BY KIND", "", func(c *ui.Ctx) { kit.Note(c, "n", "tokens · cost") }, func(c *ui.Ctx) {
		var strip []widget.ChartSeries
		for _, k := range kinds {
			strip = append(strip, widget.ChartSeries{Name: k.name, Values: []float64{k.cost(cost)}, Tone: th.C(k.tone)})
		}
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Cost by token kind", Help: "agent/stats#chart"})
		for _, k := range []int{0, 2, 1, 3, 4} {
			kit.KV(c, k, th.C(kinds[k].tone), kinds[k].name, "", kit.Count(kinds[k].tok(t)), kit.Money(kinds[k].cost(cost)))
		}
	})
}

func modelsCard(c *ui.Ctx, ms []stats.Model) {
	th := c.Theme()
	kit.StatsCard(c, "models", ui.Flex(1), "MODELS", "", func(c *ui.Ctx) { kit.Note(c, "n", "req · tokens · cost") }, func(c *ui.Ctx) {
		var strip []widget.ChartSeries
		for i, m := range ms {
			strip = append(strip, widget.ChartSeries{Name: m.Name, Values: []float64{m.Cost}, Tone: greyTone(th, i)})
		}
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Cost by model", Help: "agent/stats#chart"})
		for i, m := range ms {
			if i == 4 {
				break
			}
			sub := ""
			if m.Sidechain > m.Requests/2 {
				sub = "subagents"
			}
			kit.KV(c, i, render.Color{}, m.Name, sub, kit.Count(m.Requests), kit.Count(m.Tokens), kit.Money(m.Cost))
		}
		kit.Note(c, "n", "bar is share of cost")
	})
}

// toolsByErrors are the tools with 100 calls or more, by the share of calls that errored.
func toolsByErrors(c *ui.Ctx, tools []stats.ToolRow) {
	th := c.Theme()
	var ts []stats.ToolRow
	for _, t := range tools {
		if t.Calls >= 100 {
			ts = append(ts, t)
		}
	}
	slices.SortStableFunc(ts, func(a, b stats.ToolRow) int {
		ra, rb := float64(a.Errors)/float64(a.Calls), float64(b.Errors)/float64(b.Calls)
		switch {
		case ra > rb:
			return -1
		case ra < rb:
			return 1
		}
		return 0
	})
	kit.StatsCard(c, "tools", ui.Flex(1), "TOOLS BY ERROR RATE", "", func(c *ui.Ctx) { kit.Note(c, "n", "≥ 100 calls") }, func(c *ui.Ctx) {
		if len(ts) == 0 {
			kit.Note(c, "none", "no tool has 100 calls in this range")
		}
		for i, t := range ts {
			if i == 5 {
				break
			}
			rate := float64(t.Errors) / float64(t.Calls)
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: t.Name}, func(c *ui.Ctx) {
				c.Text("n", t.Name, kit.MonoFont(11, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("calls", kit.Count(t.Calls), kit.MonoFont(11, th.C(theme.FgMuted)))
				shareBar(c, "bar", rate/0.08, th.C(tStatsBad), 60, "")
				c.Row("pct", ui.Style{W: ui.Px(40), Main: ui.End}, func(c *ui.Ctx) {
					c.Text("t", fmt.Sprintf("%.1f%%", rate*100), kit.MonoFont(11, th.C(theme.Fg)))
				})
			})
		}
		kit.Note(c, "note", "bar is the share of calls that errored")
	})
}
