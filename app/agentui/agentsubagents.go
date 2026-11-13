package agentui

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const subagentsHelp = "agent/console#subagents"

// subagents are the helpers the agent started, newest first, in the console's side column: each opens its detail.
func (x *Console) subagents(c *ui.Ctx, se core.SessionState) {
	if len(se.Subagents) == 0 {
		return
	}
	th := c.Theme()
	st := x.state(se.ID)
	running := 0
	for _, s := range se.Subagents {
		if s.Running {
			running++
		}
	}
	now := c.Now()
	kit.Section(c, "subagents", "Subagents", fmt.Sprintf("%d running", running), func(c *ui.Ctx) {
		for i := len(se.Subagents) - 1; i >= 0; i-- {
			s := se.Subagents[i]
			if widget.Clickable(c, s.ID, ui.Style{W: ui.Flex(1), Gap: 2, Dir: ui.Column, Clip: true,
				Label: cmp.Or(s.Type, "agent") + " " + s.Description, Help: subagentsHelp}, func(c *ui.Ctx) {
				c.Row("top", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
					kit.StateMark(c, "mark", subagentMark(s), s.Started, 10)
					c.Text("t", cmp.Or(s.Description, "subagent"), kit.UIFont(12.5, th.C(theme.Fg)))
				})
				c.Row("sub", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 20}, Clip: true}, func(c *ui.Ctx) {
					c.Text("t", cmp.Or(s.Type, "agent")+" · "+subagentTook(s, now), kit.MonoFont(10, th.C(theme.FgGhost)))
				})
			}).Clicked {
				st.sub = s.ID
			}
		}
	})
	x.subagentDetail(c, se, st)
}

// subagentDetail is the subagent picked in the side column: what it was asked, where it stands, and what it said.
func (x *Console) subagentDetail(c *ui.Ctx, se core.SessionState, st *consoleState) {
	var s core.Subagent
	for _, o := range se.Subagents {
		if o.ID == st.sub {
			s = o
		}
	}
	open := s.ID != ""
	if !open {
		st.sub = ""
		return
	}
	th := c.Theme()
	widget.Modal(c, "subagent", &open, widget.ModalOpts{Title: cmp.Or(s.Description, "Subagent"), Width: 680, Help: subagentsHelp}, func(c *ui.Ctx) {
		state := "done"
		if s.Running {
			state = "running"
		}
		c.Row("state", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			kit.StateMark(c, "mark", subagentMark(s), s.Started, 10)
			widget.Chip(c, "st", state, subagentTone(c, s), subagentsHelp)
			c.Text("t", cmp.Or(s.Type, "agent")+" · "+subagentTook(s, c.Now()), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
		if !s.Started.IsZero() {
			kit.PreviewField(c, "started", s.Started.Local().Format("15:04:05"))
		}
		if !s.Ended.IsZero() {
			kit.PreviewField(c, "finished", s.Ended.Local().Format("15:04:05"))
		}
		if len(s.Threads) > 0 {
			var names []string
			for _, id := range s.Threads {
				if t, ok := se.Thread(id); ok {
					names = append(names, t.Heading())
				}
			}
			kit.PreviewField(c, "requests", strings.Join(names, " · "))
		}
		for _, part := range []struct{ key, title, body, none string }{
			{"prompt", "PROMPT", s.Prompt, "The agent gave it no prompt Oriel saw."},
			{"result", "RESULT", s.Result, map[bool]string{true: "Still running.", false: "It said nothing back."}[s.Running]},
		} {
			c.Column(part.key, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
				c.Text("h", part.title, kit.KickerFont(th.C(theme.FgFaint)))
				if strings.TrimSpace(part.body) == "" {
					c.Text("none", part.none, kit.UIFont(12.5, th.C(theme.FgGhost)))
					return
				}
				c.Column("box", ui.Style{W: ui.Flex(1), H: ui.Px(200), Pad: ui.Pad(12), Fill: th.C(theme.BgBase), Stroke: th.C(theme.Line), Border: 1,
					Radius: 6, ScrollY: true}, func(c *ui.Ctx) {
					kit.MarkdownText(c, x.h, "md", part.body, 630, kit.UIFont(12.5, th.C(theme.FgMuted)), kit.MarkdownOpts{})
				})
			})
		}
	})
	if !open {
		st.sub = ""
	}
}

func subagentMark(s core.Subagent) string {
	if s.Running {
		return kit.MarkRunning
	}
	return kit.MarkOK
}

func subagentTone(c *ui.Ctx, s core.Subagent) render.Color {
	if s.Running {
		return c.Theme().C(tToolRunning)
	}
	return c.Theme().C(kit.TToolOK)
}

// subagentTook is how long a subagent ran, or has run so far, and the tools it used: "3:04 · 17 tools".
func subagentTook(s core.Subagent, now time.Time) string {
	n := fmt.Sprintf("%d %s", s.Tools, kit.Plural(s.Tools, "tool", "tools"))
	if s.Started.IsZero() {
		return n
	}
	end := now
	if !s.Ended.IsZero() {
		end = s.Ended
	}
	d := end.Sub(s.Started)
	return fmt.Sprintf("%d:%02d · %s", int(d.Minutes()), int(d.Seconds())%60, n)
}
