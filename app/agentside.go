package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ConsoleSide prefixes the views a plugin adds to the console's right column, above its own sections, in the order
// of their names: agent.console.side.<order>-<name>. Each is built for the session in the console, its ID the
// instance.
const ConsoleSide = "agent.console.side."

// side is the console's right column: what plugins add, the session's stats, the plans' usage, what this turn
// touched, and the rules in force.
func (x *agentConsole) side(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	scrolled(c, "side", &x.state(se.ID).side, ui.Style{W: ui.Px(330), H: ui.Flex(1), Fill: th.C(tConsoleSide), Help: "agent/console#stats"}, ui.Style{},
		func(c *ui.Ctx, _ func(string) ui.ID) {
			for _, id := range x.h.Views() {
				if v, ok := x.h.ViewByID(id); ok && strings.HasPrefix(id, ConsoleSide) {
					c.Column(id, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
						c.Node(v.Build(registry.ViewCtx{Width: 330, Instance: string(se.ID)}))
					})
				}
			}
			x.sessionStats(c, se)
			x.tasks(c, se)
			x.usage(c, se)
			x.touched(c, se)
			x.rules(c, se)
		})
}

// section is one of the side's blocks: a kicker with a note at its right, then body.
func section(c *ui.Ctx, key, title, note string, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", strings.ToUpper(title), kickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if note != "" {
				c.Text("n", note, monoFont(9.5, th.C(theme.FgGhost)))
			}
		})
		body(c)
	})
	c.Box(key+".rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// stat is a row of a side section: a name, and its value at the right in tone.
func stat(c *ui.Ctx, key, name, value string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + value}, func(c *ui.Ctx) {
		c.Text("n", name, monoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("v", value, monoFont(11.5, tone))
	})
}

// tokens is a count as the session stats show it: 950, 38k, 1.42M.
func tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// hm is a span as "1h 12m", "12m", "40s".
func hm(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func (x *agentConsole) sessionStats(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	t := se.Agent.Stats
	fg := th.C(theme.Fg)
	section(c, "stats", "Session stats", se.Agent.Provider, func(c *ui.Ctx) {
		ctxv := tokens(int64(t.Context))
		if t.ContextMax > 0 {
			ctxv += " / " + tokens(int64(t.ContextMax))
		}
		stat(c, "context", "context", ctxv, fg)
		if t.ContextMax > 0 {
			widget.Meter(c, "contextbar", float64(t.Context)/float64(t.ContextMax), widget.MeterOpts{Tone: th.C(tMeterContext), Label: "context"})
		}
		stat(c, "cache", "cache hit", fmt.Sprintf("%d%%", int(t.CacheHit()*100+.5)), fg)
		stat(c, "turns", "turns · tool calls", fmt.Sprintf("%d · %d", t.Turns, t.ToolCalls), fg)
		stat(c, "tokens", "tokens in / out", tokens(t.Input+t.CacheRead+t.CacheWrite)+" / "+tokens(t.Output), fg)
		c.Row("cost", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("n", "cost (est.)", monoFont(11.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			statsLink(c, x.h, se.ID, fmt.Sprintf("$%.2f ›", t.Cost), 11.5)
		})
		run := time.Duration(t.Running) * time.Second
		if se.Agent.Working && !se.Agent.TurnStarted.IsZero() {
			run += c.Now().Sub(se.Agent.TurnStarted)
		}
		stat(c, "running", "running", hm(run), fg)
	})
}

// usage is each provider's plan windows: this session's provider first, then the others.
func (x *agentConsole) usage(c *ui.Ctx, se core.SessionState) {
	us := slices.Clone(x.h.State().Usage)
	if len(us) == 0 {
		return
	}
	slices.SortStableFunc(us, func(a, b core.ProviderUsage) int {
		switch {
		case a.Provider == se.Agent.Provider:
			return -1
		case b.Provider == se.Agent.Provider:
			return 1
		}
		return 0
	})
	th := c.Theme()
	now := c.Now()
	for i, u := range us {
		title, note := "Plan usage", cmpOr(u.Plan, providerName(u.Provider))
		if i > 0 || u.Provider != se.Agent.Provider {
			title, note = cmpOr(u.Plan, providerName(u.Provider)), "other sessions"
		}
		section(c, "usage."+u.Provider, title, note, func(c *ui.Ctx) {
			for j, w := range u.Windows {
				v := fmt.Sprintf("%d%%", int(w.Used*100+.5))
				if !w.Resets.IsZero() && w.Resets.After(now) {
					v += " · resets " + strings.ReplaceAll(hm(w.Resets.Sub(now)), " ", "")
				}
				tone := th.C(tMeterContext)
				if w.Used >= .9 {
					tone = th.C(theme.Error)
				}
				stat(c, fmt.Sprint("w", j), w.Name, v, th.C(theme.Fg))
				widget.Meter(c, fmt.Sprint("bar", j), w.Used, widget.MeterOpts{Tone: tone, Label: w.Name})
			}
			if u.Credits != "" {
				stat(c, "credits", "credits", u.Credits, th.C(theme.Fg))
			}
		})
	}
}

// turnEdits are the files the latest turn edited, with their lines added and removed, in the order first edited.
func turnEdits(se core.SessionState) []core.FileDiff {
	start := 0
	for i, it := range se.Transcript {
		if it.Kind == core.ItemUser {
			start = i
		}
	}
	var out []core.FileDiff
	for _, it := range se.Transcript[start:] {
		if it.Diff == nil {
			continue
		}
		if i := slices.IndexFunc(out, func(d core.FileDiff) bool { return d.Path == it.Diff.Path }); i >= 0 {
			out[i].Added += it.Diff.Added
			out[i].Removed += it.Diff.Removed
			continue
		}
		out = append(out, core.FileDiff{Path: it.Diff.Path, Added: it.Diff.Added, Removed: it.Diff.Removed})
	}
	return out
}

func (x *agentConsole) touched(c *ui.Ctx, se core.SessionState) {
	edits := turnEdits(se)
	if len(edits) == 0 {
		return
	}
	th := c.Theme()
	section(c, "touched", "This turn touched", "", func(c *ui.Ctx) {
		for i, d := range edits {
			c.Row(i, ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", filepath.Base(d.Path), monoFont(11.5, th.C(theme.FgMuted)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				widget.DiffStat(c, "s", d.Added, d.Removed, widget.DiffStatOpts{})
			})
		}
	})
}

// rules are what lets the agent through: its gate, the sandbox's, and the "Always" rules made in this workspace.
func (x *agentConsole) rules(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	type rule struct {
		label string
		tone  theme.TokenID
	}
	var rs []rule
	switch {
	case se.Agent.Sandboxed:
		// The agent asks nothing itself in a sandbox: the sandbox and its gateway are the gate.
		rs = append(rs, rule{"tools · auto", tRuleAllow})
	case se.Agent.Policy == "ask":
		rs = append(rs, rule{"edit · ask", tRuleAsk}, rule{"bash · ask", tRuleAsk})
	case se.Agent.Policy == "light":
		rs = append(rs, rule{"edit · auto", tRuleAllow}, rule{"bash · ask", tRuleAsk})
	case se.Agent.Policy == "bypass":
		rs = append(rs, rule{"tools · auto", tRuleAllow})
	}
	if se.Agent.Sandboxed {
		net := cmpOr(se.Agent.Network, "ask")
		tone := tRuleAsk
		if net == "open" {
			tone = tRuleAllow
		}
		rs = append(rs, rule{"net · " + net, tone}, rule{"git push · gateway", tRuleAsk})
	}
	for _, r := range se.Rules {
		rs = append(rs, rule{r, tRuleAllow})
	}
	if len(rs) == 0 {
		return
	}
	section(c, "rules", "Rules in force", "", func(c *ui.Ctx) {
		// Chips flow onto rows of about the column's width.
		var rows [][]rule
		w := 0
		for _, r := range rs {
			n := len([]rune(r.label))*7 + 26
			if len(rows) == 0 || w+n > 290 {
				rows = append(rows, nil)
				w = 0
			}
			rows[len(rows)-1] = append(rows[len(rows)-1], r)
			w += n
		}
		for i, row := range rows {
			c.Row(i, ui.Style{Gap: 6}, func(c *ui.Ctx) {
				for j, r := range row {
					widget.Chip(c, j, r.label, th.C(r.tone), "agent/console#rules")
				}
			})
		}
		if se.Agent.Sandboxed && widget.Link(c, "permissions", "Permissions and connections ›", "agent/permissions") {
			x.run(context.Background(), "agent.permissions", nil)
		}
	})
}

var (
	tTaskDone    = theme.Declare("tasks.done", "ok")
	tTaskWorking = theme.Declare("tasks.in_progress", "status.working")
	tTaskPending = theme.Declare("tasks.pending", "fg.ghost")
)

// taskCount is how many of a session's tasks are done, and how many there are.
func taskCount(ts []core.Task) (done, all int) {
	for _, t := range ts {
		if t.Status == core.TaskCompleted {
			done++
		}
	}
	return done, len(ts)
}

// taskRail is a session's tasks as the rail shows them (01): ✓ done, ◐ in progress, ○ to do.
func taskRail(c *ui.Ctx, key any, ts []core.Task) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 7, Role: ui.RoleList, Label: "Tasks"}, func(c *ui.Ctx) {
		for _, t := range ts {
			fg := th.C(theme.FgMuted)
			if t.Status == core.TaskInProgress {
				fg = th.C(theme.Fg)
			}
			c.Row(t.ID, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Role: ui.RoleListItem, Label: t.Subject + " " + string(t.Status)}, func(c *ui.Ctx) {
				taskMark(c, "g", t.Status)
				c.Text("s", truncate(t.Subject, 48), uiFont(12.5, fg))
			})
		}
	})
}

// taskMark is a task's state, drawn: a tick when done, a half-filled ring in progress, an empty one to do.
func taskMark(c *ui.Ctx, key any, s core.TaskStatus) {
	th := c.Theme()
	if s == core.TaskCompleted {
		c.Box(key, ui.Style{W: ui.Px(12)}, func(c *ui.Ctx) { c.Text("t", "✓", uiFont(12.5, th.C(tTaskDone))) })
		return
	}
	tone := th.C(tTaskPending)
	if s == core.TaskInProgress {
		tone = th.C(tTaskWorking)
	}
	c.Paint(key, ui.Px(12), ui.Px(12), func(f render.Frame, r render.Rect) {
		ring := render.Rect{Min: render.Point{X: r.Min.X + 1.5, Y: r.Min.Y + 1.5}, Max: render.Point{X: r.Max.X - 1.5, Y: r.Max.Y - 1.5}}
		f.Rect(ring, render.Style{Stroke: tone, Width: 1.2, Radius: ring.Dx() / 2})
		if s == core.TaskInProgress {
			f.PushClip(render.Rect{Min: ring.Min, Max: render.Point{X: ring.Min.X + ring.Dx()/2, Y: ring.Max.Y}}, 0)
			f.Rect(ring, render.Style{Fill: tone, Radius: ring.Dx() / 2})
			f.Pop()
		}
	})
}

func (x *agentConsole) tasks(c *ui.Ctx, se core.SessionState) {
	if len(se.Tasks) == 0 {
		return
	}
	done, all := taskCount(se.Tasks)
	section(c, "tasks", "Tasks", fmt.Sprintf("%d/%d", done, all), func(c *ui.Ctx) { taskRail(c, "rail", se.Tasks) })
}

// latestRun is the session's last test run.
func latestRun(se core.SessionState) (core.TestRun, bool) {
	if len(se.Runs) == 0 {
		return core.TestRun{}, false
	}
	last := se.Runs[0]
	for _, r := range se.Runs[1:] {
		if r.Ended.After(last.Ended) || last.Ended.IsZero() {
			last = r
		}
	}
	return last, true
}

// sessionStats is 01's session stats at the foot of the summary: context, cache and cost, then the latest tests.
func sessionStats(c *ui.Ctx, h plugin.Host, se core.SessionState, now time.Time) {
	th := c.Theme()
	t := se.Agent.Stats
	label := func(c *ui.Ctx, k, s string) { c.Text(k, s, monoFont(10.5, th.C(theme.FgFaint))) }
	value := func(c *ui.Ctx, k, s string, tone render.Color) { c.Text(k, s, monoFont(11, tone)) }
	fg := th.C(theme.Fg)
	c.Column("stats", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 12), Gap: 10, Role: ui.RoleGroup, Label: "Session stats", Help: "agent/console#stats"},
		func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("k", "SESSION", kickerFont(th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("who", strings.Join(slices.DeleteFunc([]string{strings.ToLower(providerName(se.Agent.Provider)), se.Agent.Model, se.Agent.Effort},
					func(s string) bool { return s == "" || s == "agent" }), " · "), monoFont(10, th.C(theme.FgGhost)))
			})
			c.Row("a", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				label(c, "ctx", "CTX")
				frac := 0.0
				if t.ContextMax > 0 {
					frac = float64(t.Context) / float64(t.ContextMax)
				}
				widget.Meter(c, "ctxbar", frac, widget.MeterOpts{Tone: th.C(tMeterContext), Width: ui.Px(44), Label: "context"})
				value(c, "ctxv", tokens(int64(t.Context)), fg)
				c.Box("g1", ui.Style{W: ui.Px(6)}, nil)
				label(c, "cache", "CACHE")
				value(c, "cachev", fmt.Sprintf("%d%%", int(t.CacheHit()*100+.5)), fg)
				c.Box("g2", ui.Style{W: ui.Px(6)}, nil)
				label(c, "cost", "$")
				statsLink(c, h, se.ID, fmt.Sprintf("%.2f", t.Cost), 11)
			})
			c.Row("b", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				done, all := taskCount(se.Tasks)
				label(c, "tasks", "TASKS")
				value(c, "tasksv", fmt.Sprintf("%d/%d", done, all), fg)
				c.Box("g1", ui.Style{W: ui.Px(6)}, nil)
				label(c, "tests", "TESTS")
				r, ok := latestRun(se)
				switch {
				case !ok:
					value(c, "testsv", "none yet", th.C(theme.FgGhost))
				case r.Failed > 0:
					value(c, "testsv", fmt.Sprintf("%d fail", r.Failed), th.C(theme.Error))
				default:
					value(c, "testsv", fmt.Sprintf("%d ✓", r.Passed), th.C(tToolOK))
				}
				if n := sessionLedger(h, se); n.Total() > 0 {
					c.Box("g2", ui.Style{W: ui.Px(6)}, nil)
					label(c, "reviewed", "REVIEWED")
					value(c, "reviewedv", fmt.Sprintf("%d/%d", n.Read, n.Total()), ledgerTone(th, n))
				}
				if se.Agent.Working && !se.Agent.TurnStarted.IsZero() {
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					value(c, "turn", "turn "+hm(now.Sub(se.Agent.TurnStarted)), th.C(tToolRunning))
				}
			})
		})
}

// sessionLedger is the review ledger of a session's projects together.
func sessionLedger(h plugin.Host, se core.SessionState) registry.LedgerCount {
	var n registry.LedgerCount
	for _, p := range se.Projects {
		if p.Dir != "" && !p.ReadOnly {
			l, _ := registry.LedgerOf(h, p.Dir)
			n = n.Add(l.All)
		}
	}
	return n
}

// statsLink is a session's cost as a link to its stats (a main tab), with a card saying so under the pointer.
func statsLink(c *ui.Ctx, h plugin.Host, id core.SessionID, text string, size float32) {
	th := c.Theme()
	anchor := c.Next("statslink")
	if widget.Clickable(c, "statslink", ui.Style{Label: "Open the session's stats", Help: "agent/stats#session", NoHover: true}, func(c *ui.Ctx) {
		c.Text("t", text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size, Weight: typeset.Medium}, Color: th.C(theme.Accent)})
	}).Clicked {
		if err := h.Run(context.Background(), "stats.session", registry.Args{"id": string(id)}); err != nil {
			h.Log().Error("stats", "session", id, "err", err)
		}
	}
	widget.HoverCard(c, "statscard", anchor, widget.HoverCardOpts{Label: "Session stats", Help: "agent/stats#session"}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "Session stats", uiFont(12, th.C(theme.Fg)))
			keycap(c, "k", ":Stats session")
		})
		c.Text("d", "opens as a main tab", monoFont(10, th.C(theme.FgFaint)))
	})
}
