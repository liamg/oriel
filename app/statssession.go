package app

import (
	"encoding/json"
	"fmt"
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

// The session stats tab (43's panel 1): one session's stats as a main tab, its instance the session's ID.

type sessionStatsView struct{ x *statsExt }

func (v sessionStatsView) Title(instance string) string {
	if se, ok := v.x.h.Session(core.SessionID(instance)); ok {
		return "Stats · " + se.Name()
	}
	return "Stats · " + instance
}

func (v sessionStatsView) EdgeToEdge() {}

func (v sessionStatsView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "stats." + vc.Instance, Fn: func(c *ui.Ctx) { v.x.sessionTab(c, core.SessionID(vc.Instance)) }}
}

func (v sessionStatsView) Persist(instance string) (json.RawMessage, bool) {
	b, err := json.Marshal(instance)
	return b, err == nil
}

func (v sessionStatsView) Revive(state json.RawMessage) (string, error) {
	var id string
	err := json.Unmarshal(state, &id)
	return id, err
}

// tabState is a session tab's own: whether its chart shows cost or tokens.
type tabState struct{ tokens int }

// maxSteps is how many steps the session chart draws at most; a longer session's are wider than five minutes.
const maxSteps = 150

func (x *statsExt) sessionTab(c *ui.Ctx, id core.SessionID) {
	th := c.Theme()
	d, ok := x.detail(id)
	se, _ := x.h.Session(id)
	st := ui.State[tabState](c, c.Next("tab"))
	c.Column("tab", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase), Role: ui.RoleRegion, Label: "Session stats",
		Help: "agent/stats#session"}, func(c *ui.Ctx) {
		x.tabHeader(c, se, d, st)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if !ok {
			c.Column("wait", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", "Reading the session's transcript…", uiFont(12.5, th.C(theme.FgFaint)))
			})
			return
		}
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12}, func(c *ui.Ctx) {
			sessionTiles(c, d)
			c.Row("mid", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
				x.overTime(c, d, st.tokens == 1)
				x.repoCard(c, d)
			})
			c.Row("low", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
				toolsCard(c, d.Tools, d.Totals)
				c.Column("side", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					statsCard(c, "hours", ui.Flex(1), "HOURS OF THE DAY", "", func(c *ui.Ctx) { note(c, "n", "cost by hour started") }, func(c *ui.Ctx) {
						hoursChart(c, "chart", d.Hours, 70)
					})
					effortCard(c, d.Efforts)
				})
			})
		})
	})
}

func (x *statsExt) tabHeader(c *ui.Ctx, se coreSession, d stats.Detail, st *tabState) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "STATS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		name := string(d.Session)
		var a core.AgentState
		var wid core.WorkerID
		if se != nil {
			name, a, wid = se.Name(), se.Agent(), se.Snapshot().Worker
		}
		c.Text("n", name, mono(12, typeset.SemiBold, th.C(theme.Fg)))
		var meta []string
		if a.Provider != "" {
			meta = append(meta, strings.ToLower(providerName(a.Provider)))
		}
		if d.Account != "" || d.AccountName != "" {
			meta = append(meta, x.accountLabel(d.Account, d.AccountName))
		}
		if m := strings.TrimSpace(a.Model + " " + a.Effort); m != "" {
			meta = append(meta, m)
		}
		if wid == "" {
			wid = core.LocalWorker
		}
		meta = append(meta, x.workerName(wid))
		if !d.First.IsZero() {
			first, last := d.First.In(x.loc), d.Last.In(x.loc)
			day := first.Format("Jan 2")
			if now := x.now().In(x.loc); first.YearDay() == now.YearDay() && first.Year() == now.Year() {
				day = "today"
			}
			meta = append(meta, fmt.Sprintf("%s %s – %s", day, first.Format("15:04"), last.Format("15:04")))
		}
		if a.Working || a.Running {
			meta = append(meta, "live")
		}
		c.Text("m", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		widget.Segmented(c, "mode", []string{"$ COST", "TOKENS"}, &st.tokens, widget.SegmentedOpts{Label: "Chart in", Help: "agent/stats#session"})
		if widget.Button(c, "all", "All sessions", widget.ButtonOpts{Key: ":Stats", Help: "agent/stats#page"}) {
			x.run("stats.open", nil)
		}
	})
}

type coreSession = core.Session

func sessionTiles(c *ui.Ctx, d stats.Detail) {
	th := c.Theme()
	t := d.Totals
	span := d.Last.Sub(d.First)
	c.Row("tiles", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		statsTile(c, "cost", "COST · EST.", money(t.Cost), "", func(c *ui.Ctx) {
			if t.ActiveS > 0 {
				dim(c, "s", money(t.Cost/(float64(t.ActiveS)/3600))+" / active hour")
			}
		})
		statsTile(c, "tokens", "TOKENS", count(t.Tokens.Total()), "", func(c *ui.Ctx) {
			dim(c, "s", fmt.Sprintf("cache hit %d%%", pct(cacheHit(t.Tokens))))
		})
		statsTile(c, "requests", "REQUESTS", count(t.Requests), "", func(c *ui.Ctx) {
			if t.Turns > 0 {
				dim(c, "s", fmt.Sprintf("%.1f per turn", float64(t.Requests)/float64(t.Turns)))
			}
		})
		statsTile(c, "turns", "TURNS", fmt.Sprint(t.Turns), "", func(c *ui.Ctx) { dim(c, "s", counted(d.Agents, "subagent")) })
		statsTile(c, "tools", "TOOL CALLS", count(t.ToolCalls), fmt.Sprintf("%d err", t.ToolErrors), func(c *ui.Ctx) {
			c.Text("p", fmt.Sprintf("%.1f%%", errRate(t)*100), monoFont(10, th.C(tStatsBad)))
			dim(c, "s", "errored")
		})
		active := time.Duration(t.ActiveS) * time.Second
		statsTile(c, "active", "ACTIVE", hm(active), "of "+hm(span), func(c *ui.Ctx) {
			frac := 0.0
			if span > 0 {
				frac = active.Seconds() / span.Seconds()
			}
			widget.Meter(c, "m", frac, widget.MeterOpts{Tone: th.C(tStatsHours), Label: "active"})
			dim(c, "p", fmt.Sprintf("%d%%", pct(frac)))
		})
		window := ""
		if d.Window > 0 {
			window = "/ " + tokens(d.Window)
		}
		statsTile(c, "peak", "PEAK CONTEXT", tokens(d.Peak), window, func(c *ui.Ctx) {
			if d.Window > 0 {
				widget.Meter(c, "m", float64(d.Peak)/float64(d.Window), widget.MeterOpts{Tone: th.C(tStatsOutput), Label: "peak context"})
			}
			dim(c, "s", counted(len(d.Compactions), "compact"))
		})
	})
}

// overTime is the session's cost (or tokens) per step by token kind, with idle stretches, compactions and turns.
func (x *statsExt) overTime(c *ui.Ctx, d stats.Detail, inTokens bool) {
	th := c.Theme()
	if len(d.Buckets) == 0 {
		statsCard(c, "time", ui.Flex(2.2), "COST OVER THE SESSION", "", nil, func(c *ui.Ctx) { note(c, "none", "no requests yet") })
		return
	}
	span := int64(300)
	first, last := d.Buckets[0].At, d.Buckets[len(d.Buckets)-1].At
	for (last-first)/span+1 > maxSteps {
		span *= 2
	}
	n := int((last-first)/span) + 1
	vals := make([][]float64, len(kinds))
	for k := range kinds {
		vals[k] = make([]float64, n)
	}
	turns := make([]bool, n)
	busy := make([]bool, n)
	for _, b := range d.Buckets {
		i := int((b.At - first) / span)
		busy[i] = busy[i] || b.Requests > 0
		turns[i] = turns[i] || b.Turns > 0
		for k, kd := range kinds {
			if inTokens {
				vals[k][i] += float64(kd.tok(b.Tokens))
			} else {
				vals[k][i] += kd.cost(b.Costs)
			}
		}
	}
	var series []widget.ChartSeries
	for k, kd := range kinds {
		series = append(series, widget.ChartSeries{Name: kd.name, Values: vals[k], Tone: th.C(kd.tone)})
	}
	var bands []widget.ChartBand
	for i := 0; i < n; {
		if busy[i] {
			i++
			continue
		}
		j := i
		for j < n && !busy[j] {
			j++
		}
		if gap := time.Duration(int64(j-i)*span) * time.Second; gap >= 20*time.Minute {
			bands = append(bands, widget.ChartBand{From: float64(i), To: float64(j), Label: "idle " + strings.ReplaceAll(hm(gap), " ", "")})
		}
		i = j
	}
	var marks []widget.ChartMark
	for _, cp := range d.Compactions {
		marks = append(marks, widget.ChartMark{At: float64((cp.At.Unix()-first)/span) + .5, Label: fmt.Sprintf("compacted %s → %s", tokens(cp.From),
			tokens(cp.To)), Tone: th.C(tStatsOutput), Dashed: true})
	}
	var ticks []float64
	for i, t := range turns {
		if t {
			ticks = append(ticks, float64(i))
		}
	}
	labels := make([]string, n)
	for i := range labels {
		labels[i] = time.Unix(first+int64(i)*span, 0).In(x.loc).Format("15:04")
	}
	format, yfmt, kind := money, axisMoney, "per token kind"
	if inTokens {
		format = func(v float64) string { return count(int64(v)) }
		yfmt = format
	}
	step := fmt.Sprintf("per %s", strings.ReplaceAll(hm(time.Duration(span)*time.Second), " ", ""))
	title := "COST OVER THE SESSION"
	if inTokens {
		title = "TOKENS OVER THE SESSION"
	}
	statsCard(c, "time", ui.Flex(2.2), title, step+" · by token kind", func(c *ui.Ctx) { x.legend(c, series, 0) }, func(c *ui.Ctx) {
		widget.Chart(c, "c", series, widget.ChartOpts{Height: 150, Labels: labels, YTicks: 2, YFormat: yfmt, Format: format, Hover: true, Highlight: -1,
			Bands: bands, Marks: marks, Ticks: ticks, TicksLabel: "turns", Label: "Cost over the session " + kind, Help: "agent/stats#session"})
	})
}

// repoCard is the session's cost per repo, split or whole, then its models.
func (x *statsExt) repoCard(c *ui.Ctx, d stats.Detail) {
	th := c.Theme()
	st := x.h.State()
	whole := !x.split()
	statsCard(c, "repos", ui.Flex(1), "COST PER REPO", "", func(c *ui.Ctx) {
		if widget.Toggle(c, "whole", whole, widget.ToggleOpts{Label: "Count whole session in each", Help: "agent/stats#split"}) {
			x.run("stats.split", registry.Args{"on": whole})
		}
		c.Text("l", "count whole session in each", uiFont(11.5, th.C(theme.FgMuted)))
	}, func(c *ui.Ctx) {
		var strip []widget.ChartSeries
		for i, r := range d.Repos {
			strip = append(strip, widget.ChartSeries{Name: r.Repo, Values: []float64{r.Share}, Tone: repoTone(th, st, r.Repo, i)})
		}
		if len(strip) > 0 {
			widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Cost per repo", Help: "agent/stats#split"})
		}
		var files []string
		for i, r := range d.Repos {
			kv(c, i, repoTone(th, st, r.Repo, i), r.Repo, "", fmt.Sprintf("%d%%", pct(r.Share)), money(r.Cost))
			files = append(files, fmt.Sprintf("%d in %s", r.Files, r.Repo))
		}
		switch {
		case len(d.Repos) > 1 && whole:
			note(c, "n", "each repo is charged the whole session: the rows overlap")
		case len(d.Repos) > 1:
			note(c, "n", "split by each repo's share of edits and file tool calls")
			note(c, "n2", "(Read, Edit, Write, Grep, Glob): "+strings.Join(files, ", "))
		}
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("mh", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "MODELS", capText(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			note(c, "n", "req · tokens · cost")
		})
		for i, m := range d.Models {
			sub := ""
			if m.Sidechain > m.Requests/2 {
				sub = "subagents"
			}
			kv(c, "m"+fmt.Sprint(i), greyTone(th, i), m.Name, sub, count(m.Requests), count(m.Tokens), money(m.Cost))
		}
	})
}

// toolsCard is each tool's calls, errors, how long they took and the median.
func toolsCard(c *ui.Ctx, tools []stats.ToolRow, t stats.Totals) {
	statsCard(c, "tools", ui.Flex(2.2), "TOOLS", fmt.Sprintf("%s · %s", counted(int(t.ToolCalls), "call"), counted(int(t.ToolErrors), "error")),
		func(c *ui.Ctx) { note(c, "n", "time per call") }, func(c *ui.Ctx) {
			cols := []statsCol{{"TOOL", 0}, {"CALLS", 70}, {"ERRORS", 70}, {"ERR %", 70}, {".1S 1S 10S 1M 1M+", 130}, {"P50", 70}, {"CALLS", 160}}
			tableHead(c, cols)
			most := int64(1)
			for _, tr := range tools {
				most = max(most, tr.Calls)
			}
			shown := tools
			if len(shown) > 9 {
				shown = shown[:8]
			}
			for i, tr := range shown {
				toolRow(c, i, tr, cols, most)
			}
			if rest := tools[len(shown):]; len(rest) > 0 {
				var sum stats.ToolRow
				sum.Name = fmt.Sprintf("%d more", len(rest))
				for _, tr := range rest {
					sum.Calls += tr.Calls
					sum.Errors += tr.Errors
					for j, b := range tr.Buckets {
						sum.Buckets[j] += b
					}
					sum.P50 = max(sum.P50, tr.P50)
				}
				toolRow(c, "more", sum, cols, most)
			}
		})
}

func toolRow(c *ui.Ctx, key any, tr stats.ToolRow, cols []statsCol, most int64) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center, Role: ui.RoleText, Label: tr.Name}, func(c *ui.Ctx) {
		cell(c, 0, cols[0].w, true, func(c *ui.Ctx) { c.Text("t", tr.Name, mono(11.5, typeset.Medium, th.C(theme.Fg))) })
		textCell(c, 1, cols[1].w, count(tr.Calls), th.C(theme.Fg))
		errTone := th.C(tStatsBad)
		if tr.Errors == 0 {
			errTone = th.C(theme.FgGhost)
		}
		textCell(c, 2, cols[2].w, fmt.Sprint(tr.Errors), errTone)
		rate := "–"
		if tr.Errors > 0 && tr.Calls > 0 {
			rate = fmt.Sprintf("%.1f%%", float64(tr.Errors)/float64(tr.Calls)*100)
		}
		textCell(c, 3, cols[3].w, rate, errTone)
		cell(c, 4, cols[4].w, false, func(c *ui.Ctx) { durations(c, tr.Buckets) })
		textCell(c, 5, cols[5].w, duration(tr.P50), th.C(theme.FgMuted))
		cell(c, 6, cols[6].w, true, func(c *ui.Ctx) {
			shareBar(c, "bar", float64(tr.Calls)/float64(most), th.C(tStatsBarFill), 140, "")
		})
	})
}

// durations are a tool's calls by how long they took, as five small bars: under 0.1s, 1s, 10s, a minute, longer.
func durations(c *ui.Ctx, b [stats.Buckets]int64) {
	th := c.Theme()
	v := []int64{b[0], b[1], b[2], b[3], b[4] + b[5]}
	most := slices.Max(v)
	tone := th.C(theme.FgFaint)
	c.Paint("dist", ui.Px(5*16+4*4), ui.Px(14), func(f render.Frame, r render.Rect) {
		for i, n := range v {
			h := float32(0)
			if most > 0 && n > 0 {
				h = max(2, r.Dy()*float32(n)/float32(most))
			}
			f.Rect(render.XYWH(r.Min.X+float32(i)*20, r.Max.Y-max(h, 1), 16, max(h, 1)), render.Style{Fill: theme.WithAlpha(tone, .85)})
		}
	})
}

// duration is a median as the tools table shows it: 0.1s, 4.1s, 2m 10s.
func duration(ms int64) string {
	switch {
	case ms <= 0:
		return "–"
	case ms < 100:
		return "<0.1s"
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm %02ds", ms/60_000, ms%60_000/1000)
}

func effortCard(c *ui.Ctx, es []stats.Effort) {
	th := c.Theme()
	statsCard(c, "effort", ui.Flex(1), "EFFORT", "", func(c *ui.Ctx) { note(c, "n", "req · cost") }, func(c *ui.Ctx) {
		var total int64
		for _, e := range es {
			total += e.Requests
		}
		var strip []widget.ChartSeries
		for i := len(es) - 1; i >= 0; i-- {
			strip = append(strip, widget.ChartSeries{Name: es[i].Name, Values: []float64{float64(es[i].Requests)}, Tone: greyTone(th, len(es)-1-i)})
		}
		if len(strip) > 0 {
			widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Requests by effort", Help: "agent/stats#session"})
		}
		for i := len(es) - 1; i >= 0; i-- {
			e := es[i]
			share := 0
			if total > 0 {
				share = pct(float64(e.Requests) / float64(total))
			}
			kv(c, i, render.Color{}, e.Name, "", fmt.Sprintf("%d%%", share), fmt.Sprint(e.Requests), money(e.Cost))
		}
	})
}
