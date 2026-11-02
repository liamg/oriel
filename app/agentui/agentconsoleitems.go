package agentui

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

func isShell(tool string) bool {
	switch tool {
	case "Bash", "shell", "commandExecution", "exec_command", "local_shell":
		return true
	}
	return false
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func msText(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	d := time.Duration(ms) * time.Millisecond
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// item draws one transcript item.
func (x *Console) item(c *ui.Ctx, k string, se core.SessionState, it core.Item, st *consoleState, width float32) {
	th := c.Theme()
	sel := st.sel == k
	switch it.Kind {
	case core.ItemAssistant:
		c.Row(k, ui.Style{W: ui.Px(width), Role: ui.RoleListItem, Selected: sel}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: map[bool]render.Color{true: th.C(tBlockSelected)}[sel]}, nil)
			c.Box("gap", ui.Style{W: ui.Px(6)}, nil)
			kit.DocText(c, x.h, "md", it.Text, width-8)
		})
	case core.ItemThinking:
		open := st.open(k, false)
		c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
			if widget.Clickable(c, "head", ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 7), Gap: 8, Cross: ui.Center, Help: "agent/console#thinking",
				Label: "Thinking", Expanded: &open}, func(c *ui.Ctx) {
				c.Text("t", map[bool]string{true: "▾ thinking", false: "▸ thinking"}[open], kit.MonoFont(11, th.C(tThinking)))
				c.Text("n", fmt.Sprintf("%d lines", lineCount(it.Text)), kit.MonoFont(10, th.C(tThinking)))
			}).Clicked {
				st.toggle(k)
			}
			if open {
				c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 8}}, func(c *ui.Ctx) {
					widget.Wrap(c, "w", it.Text, width-24, kit.UIFont(12, th.C(tThinking)))
				})
			}
		})
	case core.ItemUser:
		open := st.open(k, lineCount(it.Text) <= 3)
		c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
			if widget.Clickable(c, "head", ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 7), Gap: 8, Cross: ui.Center, Help: "agent/console#transcript",
				Label: "Prompt", Expanded: &open}, func(c *ui.Ctx) {
				c.Text("k", "YOU", kit.KickerFont(th.C(theme.Accent)))
				if !open {
					c.Text("t", kit.FirstLine(it.Text), kit.UIFont(12, th.C(theme.FgMuted)))
				}
			}).Clicked {
				st.toggle(k)
			}
			if open {
				c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 8}}, func(c *ui.Ctx) {
					widget.Wrap(c, "w", it.Text, width-24, kit.UIFont(12.5, th.C(theme.Fg)))
				})
			}
		})
	case core.ItemError:
		s := blockStyle(th, width, sel)
		s.Stroke, s.Pad, s.Fill = th.C(theme.Error), ui.PadXY(12, 8), theme.WithAlpha(th.C(theme.Error), .06)
		c.Column(k, s, func(c *ui.Ctx) { widget.Wrap(c, "w", it.Text, width-24, kit.UIFont(12.5, th.C(theme.Error))) })
	case core.ItemSystem:
		c.Row(k, ui.Style{W: ui.Px(width), Pad: ui.PadXY(8, 0), Role: ui.RoleListItem, Selected: sel}, func(c *ui.Ctx) {
			c.Text("t", it.Text, kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
	default:
		x.tool(c, k, se, it, st, width)
	}
}

// tool draws a tool call: a dot for how it went, its title, a summary at the right, and its edit as a mini-diff or
// its output folded.
func (x *Console) tool(c *ui.Ctx, k string, se core.SessionState, it core.Item, st *consoleState, width float32) {
	th := c.Theme()
	sel := st.sel == k
	dot := th.C(tToolRunning)
	switch {
	case it.IsError:
		dot = th.C(kit.TToolFailed)
	case it.Done:
		dot = th.C(kit.TToolOK)
	}
	title := cmp.Or(it.Title, it.Tool)
	verb, rest, _ := strings.Cut(title, " ")
	if verb == "Grep" {
		verb = "Search"
	}
	hasOutput := it.Output != "" && it.Diff == nil
	open := st.open(k, it.Diff != nil || it.IsError && hasOutput)
	var summary []string
	if it.Diff == nil && it.Done {
		if n := lineCount(it.Output); n > 0 {
			switch {
			case verb == "Search" || verb == "Find":
				summary = append(summary, fmt.Sprintf("%d %s", n, kit.Plural(n, "hit", "hits")))
			default:
				summary = append(summary, fmt.Sprintf("%d %s", n, kit.Plural(n, "line", "lines")))
			}
		}
	}
	if d := msText(it.Duration); d != "" {
		summary = append(summary, d)
	}
	c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
		head := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Help: "agent/console#tool-calls",
			Label: title}
		if hasOutput || it.Diff != nil {
			head.Expanded = &open
		}
		if widget.Clickable(c, "head", head, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: dot}, nil)
			c.Text("verb", verb, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("rest", rest, kit.MonoFont(11.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if it.Diff != nil {
				widget.DiffStat(c, "stat", it.Diff.Added, it.Diff.Removed, widget.DiffStatOpts{})
				if widget.Link(c, "diff", "view in diff ↗", "agent/console#tool-calls") {
					x.openDiff(se, it.Diff.Path)
				}
			}
			if len(summary) > 0 {
				c.Text("sum", strings.Join(summary, " · "), kit.MonoFont(10, th.C(theme.FgGhost)))
			}
			if r, ok := runOf(se, it.ID); ok {
				kit.EvidenceChip(c, "run", x.h, se.ID, r.ID, kit.RunLabel(r), r.OK)
			}
			if hasOutput && isShell(it.Tool) {
				c.Text("fold", map[bool]string{true: "▾", false: "▸"}[open], kit.MonoFont(11, th.C(theme.FgFaint)))
			}
			if widget.IconButton(c, "expand", "⤢", widget.IconButtonOpts{Label: "Show in full", Help: "agent/console#details", Size: 18}) {
				x.run(context.Background(), "agent.console.details", registry.Args{"id": string(se.ID), "item": it.ID})
			}
		}).Clicked {
			st.toggle(k)
		}
		if !open {
			return
		}
		if it.Diff != nil {
			miniDiff(c, it.Diff)
			return
		}
		if hasOutput {
			c.Column("out", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 1, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
				lines := strings.Split(strings.TrimRight(it.Output, "\n"), "\n")
				if len(lines) > 200 {
					lines = append(lines[:100:100], append([]string{fmt.Sprintf("… %d lines …", len(lines)-200)}, lines[len(lines)-100:]...)...)
				}
				tok := theme.FgMuted
				if it.IsError {
					tok = theme.Error
				}
				for i, l := range lines {
					c.Text(i, strings.ReplaceAll(l, "\t", "    "), kit.MonoFont(11.5, th.C(tok)))
				}
			})
		}
	})
}

// runOf is the test run a transcript item ran: the evidence it's a link to.
func runOf(se core.SessionState, item string) (core.TestRun, bool) {
	for _, r := range se.Runs {
		if r.Item == item && item != "" {
			return r, true
		}
	}
	return core.TestRun{}, false
}

// miniDiff is an edit's lines: numbers, the op, the text, on the diff colours.
func miniDiff(c *ui.Ctx, d *core.FileDiff) {
	th := c.Theme()
	c.Column("diff", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 2, B: 6}, Role: ui.RoleText, Label: fmt.Sprintf("+%d -%d", d.Added, d.Removed)}, func(c *ui.Ctx) {
		for i, l := range d.Lines {
			fill, num, tok := render.Color{}, l.New, theme.FgMuted
			switch l.Op {
			case "+":
				fill, tok = th.C(theme.DiffAdd), theme.Fg
			case "-":
				fill, num, tok = th.C(theme.DiffDel), l.Old, theme.Fg
			}
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(19), Pad: ui.PadXY(12, 0), Gap: 12, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
				n := ""
				if num > 0 {
					n = fmt.Sprint(num)
				}
				numTok := theme.FgGhost
				switch l.Op {
				case "+":
					numTok = kit.TToolOK
				case "-":
					numTok = kit.TToolFailed
				}
				c.Box("n", ui.Style{W: ui.Px(32)}, func(c *ui.Ctx) { c.Text("t", n, kit.MonoFont(11, th.C(numTok))) })
				c.Box("op", ui.Style{W: ui.Px(10)}, func(c *ui.Ctx) { c.Text("t", strings.TrimSpace(l.Op), kit.MonoFont(11.5, th.C(numTok))) })
				c.Text("t", strings.ReplaceAll(l.Text, "\t", "    "), kit.MonoFont(11.5, th.C(tok)))
			})
		}
	})
}

// openDiff opens an edited file's diff, or the file until something registers diff.open.
func (x *Console) openDiff(se core.SessionState, path string) {
	if !filepath.IsAbs(path) {
		if p, ok := se.Primary(); ok {
			path = filepath.Join(p.Dir, path)
		}
	}
	a := registry.Args{"path": path}
	ctx := context.Background()
	if x.h.Available("diff.open", a) {
		x.run(ctx, "diff.open", a)
		return
	}
	x.run(ctx, "editor.open", a)
}

// subagents is the box of the helpers the agent started: what each does, for how long, with how many tools.
func (x *Console) subagents(c *ui.Ctx, se core.SessionState, width float32, sel bool) {
	th := c.Theme()
	running := 0
	for _, s := range se.Subagents {
		if s.Running {
			running++
		}
	}
	now := c.Now()
	subs := se.Subagents[max(0, len(se.Subagents)-6):]
	c.Column("subagents", blockStyle(th, width, sel), func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "SUBAGENTS", kit.KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d running", running), kit.MonoFont(10, th.C(theme.FgFaint)))
		})
		for i, s := range subs {
			c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 10, Cross: ui.Center, Role: ui.RoleListItem, Label: s.Description},
				func(c *ui.Ctx) {
					tone := th.C(kit.TToolOK)
					if s.Running {
						tone = th.C(tToolRunning)
					}
					c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
					c.Text("type", cmp.Or(s.Type, "agent"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.Medium},
						Color: th.C(theme.Fg)})
					c.Text("desc", s.Description, kit.UIFont(12, th.C(theme.FgMuted)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					end := now
					if !s.Ended.IsZero() {
						end = s.Ended
					}
					el := ""
					if !s.Started.IsZero() {
						d := end.Sub(s.Started)
						el = fmt.Sprintf("%d:%02d · ", int(d.Minutes()), int(d.Seconds())%60)
					}
					c.Text("n", fmt.Sprintf("%s%d %s", el, s.Tools, kit.Plural(s.Tools, "tool", "tools")), kit.MonoFont(10, th.C(theme.FgGhost)))
				})
		}
	})
}

// background is the box of the work the last turn left running, which wakes the agent again when it's done.
func (x *Console) background(c *ui.Ctx, se core.SessionState, width float32, sel bool) {
	th := c.Theme()
	bg := se.Agent.Background
	c.Column("background", blockStyle(th, width, sel), func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center, Help: "agent/console#background"}, func(c *ui.Ctx) {
			c.Text("k", "IN THE BACKGROUND", kit.KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d running · wakes the agent when done", len(bg)), kit.MonoFont(10, th.C(theme.FgFaint)))
		})
		for i, t := range bg {
			c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 10, Cross: ui.Center, Role: ui.RoleListItem, Label: t.Description},
				func(c *ui.Ctx) {
					c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: th.C(tToolRunning)}, nil)
					c.Text("kind", cmp.Or(t.Kind, "task"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.Medium},
						Color: th.C(theme.Fg)})
					c.Text("desc", t.Description, kit.UIFont(12, th.C(theme.FgMuted)))
				})
		}
	})
}

// forkCard is a side question and its answer, from a fork that changes nothing.
func (x *Console) forkCard(c *ui.Ctx, k string, f forkAnswer, width float32, sel bool) {
	th := c.Theme()
	s := blockStyle(th, width, sel)
	s.Pad, s.Gap = ui.PadXY(14, 10), 6
	c.Column(k, s, func(c *ui.Ctx) {
		c.Row("q", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "BTW", kit.KickerFont(th.C(theme.Accent)))
			c.Text("t", f.q, kit.UIFont(12.5, th.C(theme.Fg)))
		})
		switch {
		case !f.done:
			widget.Busy(c, "wait", widget.LoadState{Phase: widget.Pending, Since: f.since}, widget.BusyOpts{Label: "Asking a fork", Size: 11,
				Help: "agent/console#fork"})
		case f.err != nil:
			c.Text("err", f.err.Error(), kit.UIFont(12, th.C(theme.Error)))
		default:
			kit.DocText(c, x.h, "a", f.a, width-28)
		}
	})
}
