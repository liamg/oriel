package app

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The global Stats page (43's panels 2 and 3): every session, grouped by repo by default, over the
// main area beside the sidebar.

// mainArea is the root view's node, which the pages cover: the main area, beside the sidebar and under the tabs.
func mainArea() ui.ID { return ui.Child(ui.Child(ui.Child(0, "shell"), "main"), RootView) }

// statsTop is how many groups the chart draws apart; the rest are one "other" series.
const statsTop = 6

func (x *statsPlugin) page(c *ui.Ctx) {
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
	widget.Page(c, "stats", &x.open, widget.PageOpts{Over: mainArea(), KeyMode: StatsKeyMode, Label: "Stats", Help: "agent/stats#page"},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.header(c, q, r, meta)
			x.toolbar(c)
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
				Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
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
					statsCard(c, "hours", ui.Flex(1), "HOURS OF THE DAY", "", func(c *ui.Ctx) { note(c, "n", "cost by hour · local") }, func(c *ui.Ctx) {
						hoursChart(c, "chart", r.Hours, 88)
					})
				})
			})
		})
}

func (x *statsPlugin) header(c *ui.Ctx, q stats.Query, r stats.Report, meta reportMeta) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
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
		meta2 := []string{scope, counted(r.Totals.Sessions, "session"), q.From.Format("Jan 2") + " – " + q.To.AddDate(0, 0, -1).Format("Jan 2")}
		c.Text("m", strings.Join(meta2, " · "), monoFont(11, th.C(theme.FgFaint)))
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
		case !meta.at.IsZero():
			read := "scanned " + ago(x.now(), meta.at) + " ago"
			if ago(x.now(), meta.at) == "now" {
				read = "scanned just now"
			}
			c.Text("scanned", read, monoFont(11, th.C(theme.FgFaint)))
		case meta.loading:
			c.Text("scanned", "scanning…", monoFont(11, th.C(theme.FgFaint)))
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

func (x *statsPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Log().Error("stats", "command", id, "err", err)
	}
}

// counted is n of what: "1 session", "64 sessions".
func counted(n int, what string) string { return fmt.Sprintf("%d %s", n, plural(n, what, what+"s")) }

// toolbar is the range, the grouping, the filter chips and the split toggle.
func (x *statsPlugin) toolbar(c *ui.Ctx) {
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
			Today: x.now(), Location: x.loc}) {
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
		c.Text("splitl", label, uiFont(11.5, th.C(theme.FgMuted)))
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// filterChips are a filter's chips: one for each value, ✕ taking it off; for the main three with none, a menu of
// what there is.
func (x *statsPlugin) filterChips(c *ui.Ctx, key string) {
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
			c.Text("k", key, monoFont(10.5, th.C(theme.FgGhost)))
			c.Text("v", label, monoFont(10.5, th.C(theme.Fg)))
			c.Text("x", "✕", monoFont(9, th.C(theme.FgGhost)))
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
func (x *statsPlugin) values(key string) []string {
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
				add(cmpOr(se.Name, string(se.ID)))
			}
		}
	}
	slices.Sort(out)
	return out
}

// filterValue is what a filter's menu shows as what the query wants: an account's or session's ID for its name.
func (x *statsPlugin) filterValue(key, v string) string {
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
func (x *statsPlugin) moreFilters() []string {
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

func (x *statsPlugin) tiles(c *ui.Ctx, q stats.Query, r stats.Report) {
	t, p := r.Totals, r.Prev
	days := int(math.Round(q.To.Sub(q.From).Hours() / 24))
	c.Row("tiles", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		statsTile(c, "cost", "COST · EST.", money(t.Cost), "", func(c *ui.Ctx) {
			delta(c, "d", t.Cost, p.Cost)
			if p.Cost > 0 {
				dim(c, "s", fmt.Sprintf("vs prev %dd %s", days, money(p.Cost)))
			}
		})
		all := t.Tokens.Total()
		statsTile(c, "tokens", "TOKENS", count(all), "", func(c *ui.Ctx) {
			delta(c, "d", float64(all), float64(p.Tokens.Total()))
			dim(c, "s", fmt.Sprintf("cache hit %d%%", pct(cacheHit(t.Tokens))))
			if out := outsideTokens(r); out > 0 && out < all {
				c.Text("o", fmt.Sprintf("%d%% outside", pct(float64(out)/float64(all))), monoFont(10, c.Theme().C(theme.FgMuted)))
			}
		})
		statsTile(c, "requests", "REQUESTS", count(t.Requests), "", func(c *ui.Ctx) {
			delta(c, "d", float64(t.Requests), float64(p.Requests))
			dim(c, "s", counted(int(t.Turns), "turn"))
		})
		statsTile(c, "sessions", "SESSIONS", fmt.Sprint(t.Sessions), "", func(c *ui.Ctx) {
			delta(c, "d", float64(t.Sessions), float64(p.Sessions))
			dim(c, "s", fmt.Sprintf("%d multi-repo", t.MultiRepo))
		})
		statsTile(c, "tools", "TOOL CALLS", count(t.ToolCalls), "", func(c *ui.Ctx) {
			rate, prev := errRate(t), errRate(p)
			if p.ToolCalls > 0 {
				d := (rate - prev) * 100
				arrow, tone := "▲", c.Theme().C(tStatsUp)
				if d < 0 {
					arrow, tone, d = "▼", c.Theme().C(tStatsDown), -d
				}
				c.Text("d", fmt.Sprintf("%s %.1fpt", arrow, d), monoFont(10, tone))
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
				dim(c, "s", money(t.Cost/(float64(t.ActiveS)/3600))+" / active hour")
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
func (x *statsPlugin) groupTones(c *ui.Ctx, r stats.Report) []render.Color {
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
			out[i] = paletteTone(th, i)
		}
		return out
	}
	for i, g := range r.Groups {
		out[i] = repoTone(th, st, g.Key, i)
	}
	return out
}

// series are the chart's: the top groups, then the rest as one.
func (x *statsPlugin) series(c *ui.Ctx, r stats.Report, tones []render.Color) []widget.ChartSeries {
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

func (x *statsPlugin) chart(c *ui.Ctx, q stats.Query, r stats.Report, tones []render.Color, w ui.Size) {
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
	statsCard(c, "chart", w, "COST PER "+what+" PER DAY", sub, func(c *ui.Ctx) {
		if q.Group == stats.BySession {
			return
		}
		x.legend(c, series, 0)
	}, func(c *ui.Ctx) {
		if q.Group == stats.BySession {
			c.Row("legend", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.legend(c, series, 4) })
		}
		if hl >= 0 && hl < len(r.Groups) && r.Totals.Cost > 0 {
			g := r.Groups[hl]
			c.Row("pick", ui.Style{W: ui.Flex(1), Main: ui.End, H: ui.Px(12)}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("%s · %s · %.1f%%", g.Label, money(g.Cost), g.Cost/r.Totals.Cost*100), monoFont(10, tones[hl]))
			})
		}
		res := widget.Chart(c, "c", series, widget.ChartOpts{Height: 140, Labels: dayLabels(r.Points), YTicks: 3, YFormat: axisMoney, Format: money, Hover: true, Highlight: hl, Title: func(i int) string {
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
func (x *statsPlugin) legend(c *ui.Ctx, series []widget.ChartSeries, perLine int) {
	th := c.Theme()
	if perLine <= 0 {
		perLine = len(series)
	}
	c.Column("legend", ui.Style{Gap: 6}, func(c *ui.Ctx) {
		for l := 0; l*perLine < len(series); l++ {
			c.Row(l, ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
				for i, s := range series[l*perLine : min(len(series), (l+1)*perLine)] {
					c.Row(i, ui.Style{Gap: 5, Cross: ui.Center}, func(c *ui.Ctx) {
						swatch(c, "sw", s.Tone)
						c.Text("n", s.Name, monoFont(10.5, th.C(theme.FgMuted)))
					})
				}
			})
		}
	})
}

// table is the groups: a row each, the selected one drilled into with ⏎ or a click.
func (x *statsPlugin) table(c *ui.Ctx, q stats.Query, r stats.Report, tones []render.Color, w ui.Size) {
	th := c.Theme()
	what := map[string]string{stats.ByRepo: "repo", stats.BySession: "session", stats.ByKind: "kind", stats.ByModel: "model",
		stats.ByProvider: "provider", stats.ByAccount: "account", stats.ByOrigin: "origin", stats.ByDay: "day"}[q.Group]
	right := func(c *ui.Ctx) {
		if x.sel < len(r.Groups) {
			c.Row("hint", ui.Style{Gap: 5, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("k", ui.Style{Pad: ui.PadXY(4, 0), Radius: 3, Stroke: theme.WithAlpha(th.C(theme.Accent), .4), Border: 1}, func(c *ui.Ctx) {
					c.Text("t", "⏎", monoFont(9, th.C(theme.Accent)))
				})
				c.Text("t", "drill into "+r.Groups[x.sel].Label, monoFont(10, th.C(theme.Accent)))
			})
		}
	}
	sorted := "sorted by cost"
	if q.Group == stats.ByDay {
		sorted = "by date"
	}
	statsCard(c, "table", w, strings.ToUpper(what)+"S", fmt.Sprintf("%s · %s", counted(len(r.Groups), what), sorted), right, func(c *ui.Ctx) {
		cols := []statsCol{{strings.ToUpper(what), 0}, {"SESSIONS", 70}, {"REQUESTS", 80}, {"TOKENS", 70}, {"COST", 80}, {"SHARE", 150},
			{"LAST ACTIVE", 90}, {"30 DAYS", 130}, {"", 20}}
		if q.Group == stats.BySession {
			cols = []statsCol{{"SESSION", 0}, {"REPOS", 200}, {"DAYS", 50}, {"REQUESTS", 80}, {"COST", 80}, {"LAST ACTIVE", 90}}
		}
		tableHead(c, cols)
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
				cell(c, 0, cols[0].w, true, func(c *ui.Ctx) { c.Text("t", fmt.Sprintf("%d more", len(rest)), monoFont(11, th.C(theme.FgFaint))) })
				if q.Group == stats.BySession {
					textCell(c, 1, cols[1].w, truncate(strings.Join(repos, ", "), 28), th.C(theme.FgGhost))
					textCell(c, 2, cols[2].w, "", th.C(theme.FgGhost))
					textCell(c, 3, cols[3].w, count(req), th.C(theme.FgMuted))
					textCell(c, 4, cols[4].w, money(cost), th.C(theme.FgMuted))
					textCell(c, 5, cols[5].w, "", th.C(theme.FgGhost))
				}
			})
		}
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.LineStrong)}, nil)
		t := r.Totals
		c.Row("total", ui.Style{W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center, Role: ui.RoleText, Label: "total"}, func(c *ui.Ctx) {
			fg := th.C(theme.Fg)
			cell(c, 0, cols[0].w, true, func(c *ui.Ctx) { c.Text("t", "total", monoFont(11, fg)) })
			if q.Group == stats.BySession {
				textCell(c, 1, cols[1].w, fmt.Sprintf("%d of %d span several repos", t.MultiRepo, t.Sessions), th.C(theme.FgGhost))
				textCell(c, 2, cols[2].w, "", fg)
				textCell(c, 3, cols[3].w, count(t.Requests), fg)
				textCell(c, 4, cols[4].w, money(t.Cost), fg)
				textCell(c, 5, cols[5].w, "", fg)
				return
			}
			textCell(c, 1, cols[1].w, fmt.Sprint(t.Sessions), fg)
			textCell(c, 2, cols[2].w, count(t.Requests), fg)
			textCell(c, 3, cols[3].w, count(t.Tokens.Total()), fg)
			textCell(c, 4, cols[4].w, money(t.Cost), fg)
			cell(c, 5, 0, true, func(c *ui.Ctx) {
				if q.Group == stats.ByRepo && q.Split {
					c.Text("t", fmt.Sprintf("%d repo-sessions from %d", t.Pairs, t.Sessions), monoFont(10.5, th.C(theme.FgGhost)))
				}
			})
		})
	})
}

func (x *statsPlugin) row(c *ui.Ctx, i int, g stats.Group, r stats.Report, cols []statsCol, tone render.Color, bySession bool) {
	th := c.Theme()
	on := i == x.sel || i == x.hover
	fill := render.Color{}
	if on {
		fill = th.C(tStatsRowOn)
	}
	in := widget.Clickable(c, "row"+g.Key, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center, Fill: fill, NoHover: true,
		Role: ui.RoleListItem, Label: g.Label, Selected: i == x.sel, Help: "agent/stats#table"}, func(c *ui.Ctx) {
		edge := render.Color{}
		if i == x.sel {
			edge = th.C(theme.Accent)
		}
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		fg, muted := th.C(theme.Fg), th.C(theme.FgMuted)
		cell(c, 0, cols[0].w, true, func(c *ui.Ctx) {
			swatch(c, "sw", tone)
			c.Text("t", g.Label, mono(11.5, typeset.Medium, fg))
			if g.Origin == stats.OriginOutside {
				c.Text("o", "outside", monoFont(10, th.C(theme.FgGhost)))
			}
		})
		if bySession {
			cell(c, 1, cols[1].w, true, func(c *ui.Ctx) {
				for j, rp := range g.Repos {
					c.Row(j, ui.Style{Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
						swatch(c, "sw", repoTone(th, x.h.State(), rp, j))
						c.Text("t", rp, monoFont(11, muted))
					})
				}
			})
			days := 0
			for _, v := range g.Series {
				if v > 0 {
					days++
				}
			}
			textCell(c, 2, cols[2].w, fmt.Sprint(days), muted)
			textCell(c, 3, cols[3].w, count(g.Requests), muted)
			textCell(c, 4, cols[4].w, money(g.Cost), fg)
			textCell(c, 5, cols[5].w, x.lastActive(g), muted)
			return
		}
		textCell(c, 1, cols[1].w, fmt.Sprint(g.Sessions), muted)
		textCell(c, 2, cols[2].w, count(g.Requests), muted)
		textCell(c, 3, cols[3].w, count(g.Tokens), muted)
		textCell(c, 4, cols[4].w, money(g.Cost), fg)
		share := 0.0
		if r.Totals.Cost > 0 {
			share = g.Cost / r.Totals.Cost
		}
		cell(c, 5, cols[5].w, true, func(c *ui.Ctx) { shareBar(c, "share", share, tone, 90, fmt.Sprintf("%.1f%%", share*100)) })
		textCell(c, 6, cols[6].w, x.lastActive(g), muted)
		cell(c, 7, cols[7].w, false, func(c *ui.Ctx) {
			widget.Chart(c, "spark", []widget.ChartSeries{{Name: g.Label, Values: g.Series, Tone: tone}}, widget.ChartOpts{Kind: widget.ChartSpark,
				Width: ui.Px(120), Highlight: -1, Label: g.Label + " by day", Help: "agent/stats#table"})
		})
		textCell(c, 8, cols[8].w, "›", th.C(theme.FgGhost))
	})
	if in.Hovered || in.Inside {
		x.hovering = i
	}
	if in.Clicked {
		x.sel = i
		x.drill(g)
	}
}

func (x *statsPlugin) lastActive(g stats.Group) string {
	if g.Last.IsZero() {
		return ""
	}
	if a := ago(x.now(), g.Last); a != "now" {
		return a + " ago"
	}
	return "now"
}

// tokensByKind is the tokens and their cost by kind, with a strip of the cost.
func tokensByKind(c *ui.Ctx, t stats.Tokens, cost stats.Costs) {
	th := c.Theme()
	statsCard(c, "kinds", ui.Flex(1), "TOKENS BY KIND", "", func(c *ui.Ctx) { note(c, "n", "tokens · cost") }, func(c *ui.Ctx) {
		var strip []widget.ChartSeries
		for _, k := range kinds {
			strip = append(strip, widget.ChartSeries{Name: k.name, Values: []float64{k.cost(cost)}, Tone: th.C(k.tone)})
		}
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Cost by token kind", Help: "agent/stats#chart"})
		for _, k := range []int{0, 2, 1, 3, 4} {
			kv(c, k, th.C(kinds[k].tone), kinds[k].name, "", count(kinds[k].tok(t)), money(kinds[k].cost(cost)))
		}
	})
}

func modelsCard(c *ui.Ctx, ms []stats.Model) {
	th := c.Theme()
	statsCard(c, "models", ui.Flex(1), "MODELS", "", func(c *ui.Ctx) { note(c, "n", "req · tokens · cost") }, func(c *ui.Ctx) {
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
			kv(c, i, render.Color{}, m.Name, sub, count(m.Requests), count(m.Tokens), money(m.Cost))
		}
		note(c, "n", "bar is share of cost")
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
	statsCard(c, "tools", ui.Flex(1), "TOOLS BY ERROR RATE", "", func(c *ui.Ctx) { note(c, "n", "≥ 100 calls") }, func(c *ui.Ctx) {
		if len(ts) == 0 {
			note(c, "none", "no tool has 100 calls in this range")
		}
		for i, t := range ts {
			if i == 5 {
				break
			}
			rate := float64(t.Errors) / float64(t.Calls)
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: t.Name}, func(c *ui.Ctx) {
				c.Text("n", t.Name, monoFont(11, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("calls", count(t.Calls), monoFont(11, th.C(theme.FgMuted)))
				shareBar(c, "bar", rate/0.08, th.C(tStatsBad), 60, "")
				c.Row("pct", ui.Style{W: ui.Px(40), Main: ui.End}, func(c *ui.Ctx) {
					c.Text("t", fmt.Sprintf("%.1f%%", rate*100), monoFont(11, th.C(theme.Fg)))
				})
			})
		}
		note(c, "note", "bar is the share of calls that errored")
	})
}
