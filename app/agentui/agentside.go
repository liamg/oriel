package agentui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ConsoleSide prefixes the views a plugin adds to the console's right column, above its own sections, in the order
// of their names: agent.console.side.<order>-<name>. Each is built for the session in the console, its ID the
// instance.
const ConsoleSide = kit.ConsoleSide

// side is the console's right column: what plugins add, the session's stats, its tasks and subagents, the plans'
// usage, and the rules in force.
func (x *Console) side(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	kit.Scrolled(c, "side", &x.state(se.ID).side, ui.Style{W: ui.Px(330), H: ui.Flex(1), Fill: th.C(tConsoleSide), Help: "agent/console#stats"}, ui.Style{},
		func(c *ui.Ctx, _ func(string) ui.ID) {
			for _, id := range x.h.Views() {
				if v, ok := x.h.ViewByID(id); ok && strings.HasPrefix(id, ConsoleSide) {
					c.Column(id, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
						c.Node(v.Build(uireg.ViewCtx{Width: 330, Instance: string(se.ID)}))
					})
				}
			}
			x.sessionStats(c, se)
			x.tasks(c, se)
			x.subagents(c, se)
			x.usage(c, se)
			x.rules(c, se)
		})
}

func (x *Console) sessionStats(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	t := se.Agent.Stats
	fg := th.C(theme.Fg)
	kit.Section(c, "stats", "Session stats", se.Agent.Provider, func(c *ui.Ctx) {
		ctxv := kit.Tokens(int64(t.Context))
		if t.ContextMax > 0 {
			ctxv += " / " + kit.Tokens(int64(t.ContextMax))
		}
		kit.Stat(c, "context", "context", ctxv, fg)
		if t.ContextMax > 0 {
			widget.Meter(c, "contextbar", float64(t.Context)/float64(t.ContextMax), widget.MeterOpts{Tone: th.C(kit.TMeterContext), Label: "context"})
		}
		kit.Stat(c, "cache", "cache hit", fmt.Sprintf("%d%%", int(t.CacheHit()*100+.5)), fg)
		kit.Stat(c, "turns", "turns · tool calls", fmt.Sprintf("%d · %d", t.Turns, t.ToolCalls), fg)
		kit.Stat(c, "tokens", "tokens in / out", kit.Tokens(t.Input+t.CacheRead+t.CacheWrite)+" / "+kit.Tokens(t.Output), fg)
		c.Row("cost", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("n", "cost (est.)", kit.MonoFont(11.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			statsLink(c, x.h, se.ID, fmt.Sprintf("$%.2f ›", t.Cost), 11.5)
		})
		run := time.Duration(t.Running) * time.Second
		if se.Agent.Working && !se.Agent.TurnStarted.IsZero() {
			run += c.Now().Sub(se.Agent.TurnStarted)
		}
		kit.Stat(c, "running", "running", kit.Duration(run), fg)
	})
}

// usage is each provider's plan windows: this session's provider first, then the others.
func (x *Console) usage(c *ui.Ctx, se core.SessionState) {
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
		title, note := "Plan usage", cmp.Or(u.Plan, services.ProviderTitle(x.h, u.Provider))
		if i > 0 || u.Provider != se.Agent.Provider {
			title, note = cmp.Or(u.Plan, services.ProviderTitle(x.h, u.Provider)), "other sessions"
		}
		kit.Section(c, "usage."+u.Provider, title, note, func(c *ui.Ctx) {
			for j, w := range u.Windows {
				v := fmt.Sprintf("%d%%", int(w.Used*100+.5))
				if !w.Resets.IsZero() && w.Resets.After(now) {
					v += " · resets " + strings.ReplaceAll(kit.Duration(w.Resets.Sub(now)), " ", "")
				}
				tone := th.C(kit.TMeterContext)
				if w.Used >= .9 {
					tone = th.C(theme.Error)
				}
				kit.Stat(c, fmt.Sprint("w", j), w.Name, v, th.C(theme.Fg))
				widget.Meter(c, fmt.Sprint("bar", j), w.Used, widget.MeterOpts{Tone: tone, Label: w.Name})
			}
			if u.Credits != "" {
				kit.Stat(c, "credits", "credits", u.Credits, th.C(theme.Fg))
			}
		})
	}
}

// rules are what lets the agent through: its gate, the sandbox's, and the "Always" rules made in this workspace.
func (x *Console) rules(c *ui.Ctx, se core.SessionState) {
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
	case se.Agent.Policy == core.PolicyAsk:
		rs = append(rs, rule{"edit · ask", tRuleAsk}, rule{"bash · ask", tRuleAsk})
	case se.Agent.Policy == core.PolicyLight:
		rs = append(rs, rule{"edit · auto", tRuleAllow}, rule{"bash · ask", tRuleAsk})
	case se.Agent.Policy == core.PolicyAuto:
		rs = append(rs, rule{"tools · the agent judges", tRuleAsk})
	case se.Agent.Policy == core.PolicyBypass:
		rs = append(rs, rule{"tools · auto", tRuleAllow})
	case se.Agent.Policy == core.PolicyRefuse:
		rs = append(rs, rule{"tools · refused unless allowed", tRuleDeny})
	}
	if se.Agent.Sandboxed {
		net := cmp.Or(se.Agent.Network, "ask")
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
	kit.Section(c, "rules", "Rules in force", "", func(c *ui.Ctx) {
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
				c.Text("s", t.Subject, kit.UIFont(12.5, fg))
			})
		}
	})
}

// taskMark is a task's state, drawn: a tick when done, a half-filled ring in progress, an empty one to do.
func taskMark(c *ui.Ctx, key any, s core.TaskStatus) {
	th := c.Theme()
	if s == core.TaskCompleted {
		c.Box(key, ui.Style{W: ui.Px(12)}, func(c *ui.Ctx) { c.Text("t", "✓", kit.UIFont(12.5, th.C(tTaskDone))) })
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

func (x *Console) tasks(c *ui.Ctx, se core.SessionState) {
	if len(se.Tasks) == 0 {
		return
	}
	done, all := taskCount(se.Tasks)
	kit.Section(c, "tasks", "Tasks", fmt.Sprintf("%d/%d", done, all), func(c *ui.Ctx) { taskRail(c, "rail", se.Tasks) })
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

// sessionStats is the session's stats at the foot of the summary: context, cache and cost, then the latest tests.
func sessionStats(c *ui.Ctx, h plugin.Host, se core.SessionState, now time.Time) {
	th := c.Theme()
	t := se.Agent.Stats
	label := func(c *ui.Ctx, k, s string) { c.Text(k, s, kit.MonoFont(10.5, th.C(theme.FgFaint))) }
	value := func(c *ui.Ctx, k, s string, tone render.Color) { c.Text(k, s, kit.MonoFont(11, tone)) }
	fg := th.C(theme.Fg)
	c.Column("stats", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 12), Gap: 10, Role: ui.RoleGroup, Label: "Session stats", Help: "agent/console#stats"},
		func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("k", "SESSION", kit.KickerFont(th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("who", strings.Join(slices.DeleteFunc([]string{strings.ToLower(services.ProviderTitle(h, se.Agent.Provider)), se.Agent.Model, se.Agent.Effort},
					func(s string) bool { return s == "" || s == "agent" }), " · "), kit.MonoFont(10, th.C(theme.FgGhost)))
			})
			c.Row("a", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				label(c, "ctx", "CTX")
				frac := 0.0
				if t.ContextMax > 0 {
					frac = float64(t.Context) / float64(t.ContextMax)
				}
				widget.Meter(c, "ctxbar", frac, widget.MeterOpts{Tone: th.C(kit.TMeterContext), Width: ui.Px(44), Label: "context"})
				value(c, "ctxv", kit.Tokens(int64(t.Context)), fg)
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
					value(c, "testsv", fmt.Sprintf("%d ✓", r.Passed), th.C(kit.TToolOK))
				}
				if se.Agent.Working && !se.Agent.TurnStarted.IsZero() {
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					value(c, "turn", "turn "+kit.Duration(now.Sub(se.Agent.TurnStarted)), th.C(tToolRunning))
				}
			})
		})
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
			c.Text("t", "Session stats", kit.UIFont(12, th.C(theme.Fg)))
			kit.Keycap(c, "k", ":Stats session")
		})
		c.Text("d", "opens as a main tab", kit.MonoFont(10, th.C(theme.FgFaint)))
	})
}
