package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The HUD's panels (ui.md 10), each contributed by the plugin that owns its data, as indicators in the hud slot.
// They read the store as they're drawn, so a store event that redraws the window shows them changed; what the store
// doesn't keep (the machine's recent load, the burn's readings, today's totals) their owners gather from its events.

var (
	tHUDLogSource = theme.Declare("hud.log.source", "violet")
	tHUDDivider   = theme.Declare("hud.divider", "line")
)

// hudFigure is a big number with its label over it and its unit after: TOKENS 4.82M.
func hudFigure(c *ui.Ctx, key any, label, value, unit string, tone render.Color) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 3, Role: ui.RoleText, Label: label + " " + value + unit}, func(c *ui.Ctx) {
		c.Text("l", label, kickerFont(th.C(theme.FgFaint)))
		c.Row("v", ui.Style{Gap: 3, Cross: ui.End}, func(c *ui.Ctx) {
			c.Text("n", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 20, Weight: typeset.Medium}, Color: tone})
			if unit != "" {
				c.Text("u", unit, monoFont(11, th.C(theme.FgFaint)))
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

func hudDot(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: tone}, nil)
}

// --- Plan usage (stats) ---

// usagePanel is every account's windows, the account in front first, each with where its burn takes it by the reset.
func (x *statsPlugin) usagePanel(c *ui.Ctx) {
	th := c.Theme()
	now := x.now()
	front, _ := x.frontUsage()
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
	if len(us) == 0 {
		c.Text("none", "no plan read yet", monoFont(10.5, th.C(theme.FgGhost)))
		return
	}
	for i, u := range us {
		if i > 0 {
			hudDivider(c, fmt.Sprint("d", i))
		}
		hist := x.usageHistory(u)
		c.Column(i, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			c.Row("h", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				parts := []string{strings.ToUpper(providerName(u.Provider)), x.accountLabel(u.Account, u.AccountName)}
				if u.Tier != "" {
					parts = append(parts, strings.ToUpper(u.Tier))
				}
				c.Text("t", strings.Join(parts, " · "), mono(10, typeset.Medium, th.C(theme.FgFaint)))
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
					c.Row("k", ui.Style{W: ui.Px(22)}, func(c *ui.Ctx) { c.Text("t", usageShort(w), monoFont(10.5, th.C(theme.FgMuted))) })
					widget.Meter(c, "m", w.Used, widget.MeterOpts{Tone: tone, Height: 6, To: r.atReset, Label: w.Name})
					fg := th.C(theme.Fg)
					if w.Used >= .7 {
						fg = th.C(theme.Warn)
					}
					c.Text("v", fmt.Sprintf("%d%%", pct(w.Used)), mono(10.5, typeset.SemiBold, fg))
					c.Text("at", x.headed(r, now), monoFont(10.5, th.C(theme.FgFaint)))
				})
				if fiveHour(w) {
					if !w.Resets.IsZero() {
						foot = append(foot, "resets "+strings.ReplaceAll(hm(w.Resets.Sub(now)), " ", ""))
					}
					foot = append(foot, fmt.Sprintf("burn %.1f%%/h", r.burn*100))
				} else if !w.Resets.IsZero() {
					foot = append(foot, "resets "+w.Resets.In(x.loc).Format("Mon 15:04"))
				}
			}
			if len(foot) > 0 {
				c.Row("foot", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 32}, Main: ui.SpaceBetween}, func(c *ui.Ctx) {
					for k, s := range foot {
						c.Text(k, s, monoFont(9.5, th.C(theme.FgGhost)))
					}
				})
			}
		})
	}
}

// usageStrip is mission control's: each account's windows, the account in front first.
func (x *statsPlugin) usageStrip(c *ui.Ctx) {
	th := c.Theme()
	front, _ := x.frontUsage()
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
	c.Row("usage", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
		for i, u := range us {
			title := joinParts("Usage", providerName(u.Provider)+strings.TrimSuffix(" "+u.Tier, " "))
			missionCell(c, i, title, func(c *ui.Ctx) {
				c.Row("w", ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
					for j, w := range u.Windows {
						if qualifier(w) != "" {
							continue
						}
						c.Row(j, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
							c.Text("k", usageShort(w), monoFont(10, th.C(theme.FgMuted)))
							widget.Meter(c, "m", w.Used, widget.MeterOpts{Tone: windowTone(th, w), Width: ui.Px(56), Height: 5, Label: w.Name})
							c.Text("v", fmt.Sprintf("%d%%", pct(w.Used)), mono(10.5, typeset.SemiBold, th.C(theme.Fg)))
						})
					}
				})
			})
		}
	})
}

// headed is where a window's burn takes it, shortly: "→59% at reset", "→97% Thu", "idle", "full Thu 14:38".
func (x *statsPlugin) headed(r windowRead, now time.Time) string {
	switch {
	case !r.runsOut.IsZero():
		return "full " + r.runsOut.In(x.loc).Format("Mon 15:04")
	case r.burn == 0:
		return "idle"
	case fiveHour(r.w):
		return fmt.Sprintf("→%d%% at reset", pct(r.atReset))
	case !r.w.Resets.IsZero() && r.w.Resets.Sub(now) > 20*time.Hour:
		return fmt.Sprintf("→%d%% %s", pct(r.atReset), r.w.Resets.In(x.loc).Format("Mon"))
	}
	return fmt.Sprintf("→%d%%", pct(r.atReset))
}

// usageNote is the panel's note: how many accounts, and how long ago they were read.
func (x *statsPlugin) usageNote() string {
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
	return counted(len(us), "account") + " · read " + agoText(x.now(), read)
}

// --- Today (stats) ---

// todayPanel is today's totals: tokens, cost, the cache's share, the requests opened today and
// the review ledger; then the hud.today slot's indicators.
func (x *statsPlugin) todayPanel(c *ui.Ctx) {
	th := c.Theme()
	now := x.now().In(x.loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.loc)
	r, _ := x.report(stats.Query{From: from, To: from.AddDate(0, 0, 1), Offset: x.offset(), Split: true})
	tok := r.Totals.Tokens
	cache := 0.0
	if p := tok.Prompt(); p > 0 {
		cache = float64(tok.CacheRead) / float64(p)
	}
	st := x.h.State()
	threads := 0
	var ledger registry.LedgerCount
	seen := map[string]bool{}
	for _, s := range st.Sessions {
		for _, t := range s.Threads {
			if !t.Created.Before(from) {
				threads++
			}
		}
		for _, p := range s.Projects {
			if p.Dir == "" || seen[p.Dir] {
				continue
			}
			seen[p.Dir] = true
			if l, ok := registry.LedgerOf(x.h, p.Dir); ok {
				ledger = ledger.Add(l.All)
			}
		}
	}
	total := tokens(tok.Total())
	unit := ""
	if n := len(total); n > 0 && (total[n-1] == 'M' || total[n-1] == 'k') {
		total, unit = total[:n-1], total[n-1:]
	}
	dollars, cents, _ := strings.Cut(fmt.Sprintf("%.2f", r.Totals.Cost), ".")
	c.Row("a", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		hudFigure(c, "tokens", "TOKENS", total, unit, th.C(theme.Fg))
		hudFigure(c, "cost", "COST", "$"+dollars, "."+cents, th.C(theme.Fg))
		hudFigure(c, "cache", "CACHE", fmt.Sprint(pct(cache)), "%", th.C(theme.Fg))
	})
	c.Row("b", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		hudFigure(c, "threads", "THREADS", fmt.Sprint(threads), "", th.C(theme.Fg))
		hudFigure(c, "reviewed", "REVIEWED", fmt.Sprint(ledger.Read), "ln", th.C(theme.OK))
		hudFigure(c, "unread", "UNREAD", fmt.Sprint(ledger.NotRead()), "ln", th.C(theme.Warn))
	})
	if inds := registry.Indicators(x.h, registry.SlotHUDToday); len(inds) > 0 && x.sh.hud != nil {
		hudDivider(c, "div")
		for _, ind := range inds {
			if n := x.sh.hud.indicator(c, ind); n != nil {
				c.Row(ind.Name, ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(n) })
			}
		}
	}
}

// --- Sessions (the session tabs) ---

var statusOrder = []core.Status{core.StatusNeedsYou, core.StatusPermission, core.StatusWorking, core.StatusCI, core.StatusReviewer,
	core.StatusQuiet, core.StatusOff}

// why is what a session's status is about, shortly: what it asks, what it's running, when it last replied.
func why(s core.SessionState, now time.Time) string {
	switch s.Status {
	case core.StatusPermission, core.StatusNeedsYou:
		if len(s.Asks) > 0 {
			a := s.Asks[0]
			if a.Tool != "" {
				return a.Tool + ": " + firstLine(a.Summary)
			}
			return firstLine(cmpOr(a.Summary, a.Host))
		}
		if s.Agent.Error != "" {
			return firstLine(s.Agent.Error)
		}
		for _, it := range slices.Backward(s.Transcript) {
			if !it.Time.IsZero() {
				return "replied " + ago(now, it.Time)
			}
		}
	case core.StatusWorking:
		if st := s.Agent.Starting; st != nil {
			return "starting · " + st.Step
		}
		n := 0
		for _, a := range s.Subagents {
			if a.Running {
				n++
			}
		}
		if n > 0 {
			return counted(n, "subagent")
		}
		for _, it := range slices.Backward(s.Transcript) {
			if it.Kind == core.ItemTool && !it.Done {
				return toolLine(it) + " · " + elapsed(now.Sub(it.Time))
			}
		}
		if !s.Agent.TurnStarted.IsZero() {
			return "working · " + elapsed(now.Sub(s.Agent.TurnStarted))
		}
	case core.StatusCI:
		return "CI running"
	case core.StatusReviewer:
		return "in review"
	}
	return ""
}

// toolLine is a tool call as one short line: its title, or the command it runs.
func toolLine(it core.Item) string {
	if it.Title != "" {
		return firstLine(it.Title)
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(it.Input), &in) == nil && in.Command != "" {
		return firstLine(in.Command)
	}
	return it.Tool
}

// elapsed is a span as m:ss.
func elapsed(d time.Duration) string {
	d = max(d, 0)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

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
		return slices.Index(statusOrder, a.Status) - slices.Index(statusOrder, b.Status)
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
				c.Text("u", f.unit, monoFont(11, th.C(theme.FgFaint)))
			})
		}
	})
	var strip []widget.ChartSeries
	for _, s := range statusOrder {
		if n := counts[s]; n > 0 {
			strip = append(strip, widget.ChartSeries{Name: statusName(s), Values: []float64{float64(n)}, Tone: th.C(statusTokens[s])})
		}
	}
	if len(strip) > 0 {
		widget.Chart(c, "strip", strip, widget.ChartOpts{Kind: widget.ChartStrip, Height: 8, Label: "Sessions by status"})
	}
	for _, s := range ss[:min(len(ss), 5)] {
		if widget.Clickable(c, s.ID, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(19), Gap: 8, Cross: ui.Center, Label: cmpOr(s.Name, string(s.ID)),
			Help: hudHelp + "#sessions"}, func(c *ui.Ctx) {
			hudDot(c, "dot", th.C(statusTokens[s.Status]))
			c.Text("n", cmpOr(s.Name, string(s.ID)), monoFont(10.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("w", why(s, now), monoFont(10.5, th.C(theme.FgFaint)))
		}).Clicked {
			h.Run(context.Background(), "session.focus", registry.Args{"id": string(s.ID)})
		}
	}
}

func statusName(s core.Status) string { return strings.ReplaceAll(string(s), "_", " ") }

// --- Logs (the notices) ---

// logsPanel is the log's tail, newest first: info and up, from every source.
func (n *notices) logsPanel(c *ui.Ctx) {
	th := c.Theme()
	rows := slices.DeleteFunc(n.panel.rows(n.h.State()), func(r logRow) bool { return bucket(r.level) < slog.LevelInfo })
	rows = rows[max(0, len(rows)-11):]
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
		for i, r := range slices.Backward(rows) {
			tg := tags[bucket(r.level)]
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(16), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: r.plain()}, func(c *ui.Ctx) {
				c.Text("t", r.time.Local().Format("15:04:05.000"), monoFont(10.5, th.C(theme.FgGhost)))
				c.Text("l", tg.tag, mono(10.5, typeset.SemiBold, th.C(tg.tok)))
				c.Row("s", ui.Style{W: ui.Px(110), Clip: true}, func(c *ui.Ctx) {
					c.Text("s", where(r), monoFont(10.5, th.C(tHUDLogSource)))
				})
				c.Row("m", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("m", firstLine(r.text), monoFont(10.5, th.C(theme.FgMuted)))
				})
			})
		}
	})
	c.Row("foot", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Link(c, "open", "Open the log ↗", hudHelp+"#logs") {
			n.h.Run(context.Background(), "Log", nil)
		}
	})
}

// --- CI queue (ship) ---

// ciRow is a pull request of a session's project with its checks.
type ciRow struct {
	project string
	pr      int
	state   string
	note    string
}

// ciRows are the pull requests the Ship page has read, failing first, then running, then passing.
func (x *shipPlugin) ciRows(now time.Time) []ciRow {
	x.mu.Lock()
	var out []ciRow
	for _, r := range x.reports {
		for _, s := range r.ships {
			if s.PR == nil || s.PR.Merged {
				continue
			}
			row := ciRow{project: s.Project, pr: s.PR.Number, state: ciOf(s.Checks)}
			done := 0
			var started time.Time
			for _, ch := range s.Checks {
				switch ch.State() {
				case gh.CheckFail:
					if row.note == "" {
						row.note = ch.Name + " ✕"
					}
					done++
				case gh.CheckPending:
					if !ch.Started.IsZero() && (started.IsZero() || ch.Started.Before(started)) {
						started = ch.Started
					}
				default:
					done++
				}
			}
			switch row.state {
			case gh.CheckPending:
				row.note = fmt.Sprintf("%d/%d", done, len(s.Checks))
				if !started.IsZero() {
					row.note += " · " + strings.ReplaceAll(hm(now.Sub(started)), " ", "")
				}
			case gh.CheckPass:
				row.note = "passed"
			case "":
				row.note = "no checks"
			}
			out = append(out, row)
		}
	}
	x.mu.Unlock()
	rank := map[string]int{gh.CheckFail: 0, gh.CheckPending: 1, gh.CheckPass: 2}
	slices.SortStableFunc(out, func(a, b ciRow) int {
		if d := rank[a.state] - rank[b.state]; d != 0 {
			return d
		}
		return b.pr - a.pr
	})
	return out
}

func (x *shipPlugin) ciPanel(c *ui.Ctx) {
	th := c.Theme()
	rows := x.ciRows(c.Now())
	if len(rows) == 0 {
		c.Text("none", "no pull requests read", monoFont(10.5, th.C(theme.FgGhost)))
		return
	}
	tone := map[string]theme.TokenID{gh.CheckFail: theme.StatusNeedsYou, gh.CheckPending: theme.StatusCI, gh.CheckPass: theme.StatusQuiet}
	for i, r := range rows {
		t, ok := tone[r.state]
		if !ok {
			t = theme.StatusOff
		}
		c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText,
			Label: fmt.Sprintf("#%d %s %s", r.pr, r.project, r.note)}, func(c *ui.Ctx) {
			hudDot(c, "dot", th.C(t))
			c.Text("n", fmt.Sprintf("#%d %s", r.pr, r.project), monoFont(11, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("w", r.note, monoFont(10.5, th.C(theme.FgFaint)))
		})
	}
}

func (x *shipPlugin) ciNote() string {
	if n := len(x.ciRows(x.now())); n > 0 {
		return counted(n, "PR")
	}
	return ""
}
