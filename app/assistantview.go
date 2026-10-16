package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/assistant"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tAsstKicker  = theme.Declare("assistant.kicker", "accent")
	tAsstModel   = theme.Declare("assistant.model", "accent")
	tAsstAvatar  = theme.Declare("assistant.avatar", "blue")
	tAsstMine    = theme.DeclareOn("assistant.bubble.mine", "alpha(fg, 5%)", "bg.base")
	tAsstTool    = theme.Declare("assistant.tool", "ok")
	tAsstRow     = theme.DeclareOn("assistant.row", "alpha(fg, 3%)", "bg.base")
	tAsstRowLead = theme.Declare("assistant.row.first", "alpha(accent, 55%)")
	tAsstDone    = theme.Declare("assistant.done", "ok")
	tAsstAsks    = theme.Declare("assistant.asks", "warn")
	tAsstNever   = theme.Declare("assistant.never", "error")
)

// drawer is the assistant's drawer (ui.md 11): the conversation and its composer, and beside them what it can do,
// what it reads and how fresh that is, and what it did lately.
func (x *assistantPlugin) drawer(c *ui.Ctx) {
	if !x.open {
		return
	}
	size := c.Size()
	height := max(360, size.Y*.68-titleH)
	widget.Drawer(c, "assistant", widget.DrawerOpts{Top: titleH, Pinned: true, Label: "Assistant", Help: assistantHelp, KeyMode: AssistantKeyMode},
		func(c *ui.Ctx) {
			th := c.Theme()
			side := min(320, size.X*.26)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Px(height), Gap: 16}, func(c *ui.Ctx) {
				c.Column("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					x.header(c)
					x.chat(c, size.X-side-80)
					x.composer(c)
				})
				c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				x.side(c, side)
			})
		})
}

// header is COMMS, what it runs on, what it reads and does, and its keys.
func (x *assistantPlugin) header(c *ui.Ctx) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tAsstKicker)}, nil)
		c.Text("k", "COMMS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Weight: typeset.Bold, Tracking: .22},
			Color: th.C(theme.Fg)})
		widget.Chip(c, "who", "assistant", th.C(theme.FgMuted), assistantHelp)
		widget.Chip(c, "model", x.runsOn(), th.C(tAsstModel), assistantHelp+"#model")
		c.Row("about", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", x.about(), monoFont(10.5, th.C(theme.FgFaint)))
		})
		keycap(c, "toggle", "⌘/")
		c.Text("toggle-t", "toggle", monoFont(10.5, th.C(theme.FgFaint)))
		keycap(c, "esc", "esc")
		c.Text("esc-t", "back to editor", monoFont(10.5, th.C(theme.FgFaint)))
		if widget.Button(c, "new", "New conversation", widget.ButtonOpts{Ghost: true, Help: assistantHelp + "#conversation"}) {
			x.conv.Clear()
		}
		if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Put the assistant away", Help: assistantHelp, Size: 18}) {
			x.setOpen(false)
		}
	})
}

// about says what it reads and how it acts.
func (x *assistantPlugin) about() string {
	return "reads " + strings.Join(x.sources(), ", ") + " · acts, asks before starting or stopping an agent"
}

// sources are what its tools read, in order of first mention.
func (x *assistantPlugin) sources() []string {
	var out []string
	for _, t := range registry.AgentToolsFor(x.h, assistantAgent, true) {
		for _, p := range append([]registry.AgentTool{t}, registry.AgentToolParts(x.h, t.Name, assistantAgent, true)...) {
			if p.Reads != "" && !slices.Contains(out, p.Reads) {
				out = append(out, p.Reads)
			}
		}
	}
	return out
}

// turn is one of the assistant's turns as the drawer draws it: the tools it used, then what it said and did.
type asstBlock struct {
	tools   []string
	entries []assistant.Entry
}

// chat is the conversation: your turns at the right; the assistant's at the left, its tools as chips, its answer,
// the rows it offers, and its confirmations and actions.
func (x *assistantPlugin) chat(c *ui.Ctx, width float32) {
	th := c.Theme()
	log, busy := x.conv.Log()
	now := c.Now()
	latestItems := -1
	for i, e := range slices.Backward(log) {
		if e.Kind == assistant.KindReply && len(e.Items) > 0 {
			latestItems = len(log) - 1 - i
			_ = e
			break
		}
	}
	wake := time.Time{}
	scrolled(c, "chat", &x.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Role: ui.RoleList, Label: "Conversation", Help: assistantHelp},
		ui.Style{Gap: 12, Pad: ui.Insets{T: 4, B: 8, R: 8}}, func(c *ui.Ctx, _ func(string) ui.ID) {
			if len(log) == 0 {
				widget.Wrap(c, "empty", "Ask what needs you, to review a pull request, to start or stop a session, or to run a job. "+
					"It reads before it answers, and anything that starts or stops an agent waits for your yes.", width*.8, uiFont(13, th.C(theme.FgGhost)))
			}
			var block *asstBlock
			flush := func(key int) {
				if block != nil {
					x.theirs(c, key, block, width, latestItems, log, now, &wake)
					block = nil
				}
			}
			for i, e := range log {
				switch e.Kind {
				case assistant.KindUser:
					flush(i)
					x.mine(c, i, e, width)
				case assistant.KindDivider:
					flush(i)
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("l", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
						c.Text("t", strings.ToUpper(e.Text), kickerFont(th.C(theme.FgGhost)))
						c.Box("r", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					})
				case assistant.KindTool:
					if block == nil {
						block = &asstBlock{}
					}
					if !slices.Contains(block.tools, e.Tool) {
						block.tools = append(block.tools, e.Tool)
					}
				case assistant.KindProgress:
					if busy && i == len(log)-1 {
						if block == nil {
							block = &asstBlock{}
						}
						block.entries = append(block.entries, e)
					}
				default:
					if block == nil {
						block = &asstBlock{}
					}
					block.entries = append(block.entries, e)
				}
			}
			flush(len(log))
			if busy && (len(log) == 0 || log[len(log)-1].Kind != assistant.KindProgress) {
				c.Text("thinking", "thinking…", monoFont(11, th.C(theme.FgFaint)))
			}
		})
	if !wake.IsZero() {
		c.WakeAt(wake)
	}
}

// mine is one of your turns: a bubble at the right.
func (x *assistantPlugin) mine(c *ui.Ctx, key any, e assistant.Entry, width float32) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Column("b", ui.Style{Pad: ui.PadXY(13, 9), Radius: 8, Fill: th.C(tAsstMine), Stroke: th.C(theme.LineStrong), Border: 1,
			Role: ui.RoleText, Label: e.Text}, func(c *ui.Ctx) {
			widget.Wrap(c, "t", e.Text, width*.6, uiFont(13, th.C(theme.Fg)))
		})
	})
}

// theirs is one of the assistant's turns: its avatar and tool chips, then what it said and did.
func (x *assistantPlugin) theirs(c *ui.Ctx, key any, b *asstBlock, width float32, latestItems int, log []assistant.Entry, now time.Time, wake *time.Time) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		c.Box("av", ui.Style{W: ui.Px(22), H: ui.Px(22), Radius: 5, Fill: th.C(tAsstAvatar), Main: ui.Center, Cross: ui.Center, Role: ui.RoleText,
			Label: "Assistant"}, func(c *ui.Ctx) {
			c.Text("g", "◇", mono(12, typeset.Bold, th.C(theme.AccentInk)))
		})
		c.Column("turn", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			if len(b.tools) > 0 {
				c.Row("tools", ui.Style{Gap: 6, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
					for i, t := range b.tools {
						widget.Chip(c, i, "✓ "+t, th.C(tAsstTool), assistantHelp+"#tools")
					}
				})
			}
			for _, e := range b.entries {
				switch e.Kind {
				case assistant.KindReply:
					widget.Rich(c, e.Seq, mdSpans(e.Text), width-40, uiFont(13.5, th.C(theme.Fg)), widget.RichOpts{})
					if len(e.Items) > 0 {
						keyed := slices.IndexFunc(log, func(o assistant.Entry) bool { return o.Seq == e.Seq }) == latestItems
						x.items(c, fmt.Sprint("items", e.Seq), e.Items, keyed)
					}
				case assistant.KindProgress:
					c.Text(e.Seq, e.Text, monoFont(11, th.C(theme.FgFaint)))
				case assistant.KindError:
					widget.Wrap(c, e.Seq, e.Text, width-40, monoFont(11, th.C(theme.Error)))
				case assistant.KindConfirm:
					x.confirmCard(c, e, now, wake)
				case assistant.KindAction:
					x.actionCard(c, e, now, wake)
				}
			}
		})
	})
}

// items are a reply's rows: each opens its session or link, or asks about it; the latest reply's have keys.
func (x *assistantPlugin) items(c *ui.Ctx, key any, items []assistant.Item, keyed bool) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		for i, it := range items {
			stroke := th.C(theme.Line)
			if i == 0 {
				stroke = th.C(tAsstRowLead)
			}
			if widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 12, Cross: ui.Center, Radius: 6,
				Fill: th.C(tAsstRow), Stroke: stroke, Border: 1, Label: it.Title, Help: assistantHelp + "#rows"}, func(c *ui.Ctx) {
				tone := th.C(theme.FgFaint)
				if t, ok := statusTokens[core.Status(it.Status)]; ok {
					tone = th.C(t)
				}
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: tone}, nil)
				c.Text("title", it.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				c.Text("text", it.Text, uiFont(12.5, th.C(theme.FgMuted)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if it.Meta != "" {
					c.Text("meta", it.Meta, monoFont(10.5, th.C(theme.FgGhost)))
				}
				if keyed && i < 9 {
					keycap(c, "key", fmt.Sprintf("⌘%d", i+1))
				}
			}).Clicked {
				x.pickRow(it)
			}
		}
	})
}

func (x *assistantPlugin) pickRow(it assistant.Item) {
	for i, o := range x.lastItems() {
		if o == it {
			x.report(x.pick(i + 1))
			return
		}
	}
	x.conv.Say("Tell me more about "+it.Title, false)
}

func (x *assistantPlugin) report(err error) {
	if err != nil {
		x.h.Notify(core.Notice{ID: "assistant", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}

// confirmCard is an act waiting for your yes: what it will do, with Yes (y) and No (n). It takes focus as it comes,
// so the keys answer it.
func (x *assistantPlugin) confirmCard(c *ui.Ctx, e assistant.Entry, now time.Time, wake *time.Time) {
	th := c.Theme()
	cf, _ := e.Card.(registry.Confirmation)
	if e.Answer != "" {
		tone, text := th.C(theme.FgFaint), map[assistant.Answer]string{assistant.Yes: "you said yes: " + e.Text,
			assistant.No: "you said no — nothing was done", assistant.NoAnswer: "no answer in time — nothing was done"}[e.Answer]
		if e.Answer == assistant.Yes {
			tone = th.C(tAsstDone)
		}
		c.Text(e.Seq, cmpOr(text, "not answered — nothing was done"), monoFont(11, tone))
		return
	}
	key := fmt.Sprint("confirm", e.Seq)
	if id := c.Next(key); !x.focused[e.ID] {
		if c.FocusWithin(id) {
			x.focused[e.ID] = true
		} else {
			c.Focus(id)
		}
	}
	left := e.Until.Sub(now).Round(time.Second)
	if left > 0 {
		*wake = earliest(*wake, now.Add(time.Second))
	}
	res := widget.ActionCard(c, key, widget.ActionCardOpts{Kicker: "Confirm · " + e.Tool, Title: cmpOr(cf.Title, "Confirm"), Detail: cf.Lead,
		Meta: fmt.Sprintf("%ds", int(max(0, left.Seconds()))), Tone: th.C(tAsstAsks), Note: cf.Note, Help: assistantHelp + "#confirm",
		Actions: []widget.CardAction{{Label: "Yes", Key: "y", Primary: true}, {Label: "No", Key: "n"}}}, func(c *ui.Ctx) { confirmBody(c, x, cf) })
	if res.Action >= 0 {
		x.report(x.answer(e.ID, res.Action == 0))
	}
}

// actionCard is something its tools did: Open what it made, and Undo with its countdown while it can be.
func (x *assistantPlugin) actionCard(c *ui.Ctx, e assistant.Entry, now time.Time, wake *time.Time) {
	th := c.Theme()
	var actions []widget.CardAction
	open, undo := -1, -1
	if e.Open != "" && !e.Undone {
		open = len(actions)
		actions = append(actions, widget.CardAction{Label: "Open", Key: "o", Primary: true})
	}
	left := e.Until.Sub(now)
	if !e.Until.IsZero() && left > 0 && !e.Undone && e.UndoErr == "" {
		undo = len(actions)
		actions = append(actions, widget.CardAction{Label: fmt.Sprintf("Undo %ds", int(left.Seconds()+.999)), Key: "u"})
		*wake = earliest(*wake, now.Add(left%time.Second+time.Millisecond))
	}
	title, tone := "✓ "+e.Text, th.C(tAsstDone)
	switch {
	case e.Undone:
		title, tone = "undone: "+e.Text, th.C(theme.FgFaint)
	case e.UndoErr != "":
		title = e.Text + " (couldn't undo: " + e.UndoErr + ")"
	}
	res := widget.ActionCard(c, fmt.Sprint("act", e.Seq), widget.ActionCardOpts{Title: title, Detail: e.Detail, Tone: tone, Inline: true,
		Actions: actions, Help: assistantHelp + "#undo"}, nil)
	switch {
	case res.Action < 0:
	case res.Action == open:
		x.report(x.show(context.Background(), e.ID))
	case res.Action == undo:
		x.report(x.undo(e.ID))
	}
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

// composer is where you type to it, or dictate.
func (x *assistantPlugin) composer(c *ui.Ctx) {
	th := c.Theme()
	c.Row("composer", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 10, Cross: ui.Center, Radius: 8, Fill: th.C(theme.BgBase),
		Stroke: theme.WithAlpha(th.C(tAsstKicker), .45), Border: 1}, func(c *ui.Ctx) {
		in := c.Next("say")
		if x.focus && len(x.conv.Waiting()) == 0 {
			c.Focus(in)
			x.focus = false
		}
		r := widget.Input(c, "say", &x.draft, widget.InputOpts{Label: "Ask the assistant", Placeholder: "Ask, or tell it what to do",
			Help: assistantHelp + "#composer", KeyMode: AssistantKeyMode})
		if r.Submitted {
			x.send()
		}
		if r.Cancelled {
			x.setOpen(false)
		}
		if x.h.Available("voice.dictate", nil) && widget.Button(c, "dictate", "dictate", widget.ButtonOpts{Ghost: true, Key: "⌃⇧␣",
			Help: "agent/voice#dictation"}) {
			c.Focus(in)
			x.report(x.h.Run(context.Background(), "voice.dictate", nil))
		}
		if x.conv.Busy() {
			if widget.Button(c, "stop", "Stop", widget.ButtonOpts{Key: "⌘.", Help: assistantHelp + "#composer"}) {
				x.report(x.conv.Interrupt(context.Background()))
			}
			return
		}
		if widget.Button(c, "send", "Send", widget.ButtonOpts{Primary: true, Key: "⏎", Disabled: strings.TrimSpace(x.draft) == "",
			Help: assistantHelp + "#composer"}) {
			x.send()
		}
	})
}

// side is what it can do, its sources and how fresh each is, what it did lately, and how it listens.
func (x *assistantPlugin) side(c *ui.Ctx, width float32) {
	th := c.Theme()
	now := c.Now()
	log, _ := x.conv.Log()
	heading := func(c *ui.Ctx, key any, s string) {
		c.Text(key, s, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .16},
			Color: th.C(theme.FgFaint)})
	}
	row := func(c *ui.Ctx, key any, label, value string, tone render.Color, lead func(c *ui.Ctx)) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: label + " " + value}, func(c *ui.Ctx) {
			if lead != nil {
				lead(c)
			}
			c.Text("l", label, monoFont(11, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("v", value, mono(11, typeset.SemiBold, tone))
		})
	}
	c.Column("side", ui.Style{W: ui.Px(width), H: ui.Flex(1), Gap: 9, Clip: true}, func(c *ui.Ctx) {
		heading(c, "can", "WHAT IT CAN DO")
		for _, t := range registry.AgentToolsFor(x.h, assistantAgent, true) {
			if t.ReadOnly || t.Name == "reply" {
				continue
			}
			value, tone := "✓", th.C(tAsstDone)
			if t.Confirm != nil {
				value, tone = "asks first", th.C(tAsstAsks)
			}
			row(c, "can."+t.Name, strings.ReplaceAll(t.Name, "_", " "), value, tone, nil)
		}
		row(c, "never", "edit code, run commands, post anywhere", "never", th.C(tAsstNever), nil)

		c.Box("gap1", ui.Style{H: ui.Px(6)}, nil)
		heading(c, "src", "SOURCES")
		read := x.lastRead(log)
		for _, s := range x.sources() {
			fresh := "—"
			if at, ok := read[s]; ok {
				fresh = ago(now, at)
			}
			row(c, "src."+s, s, fresh, th.C(theme.FgMuted), func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: th.C(theme.OK)}, nil)
			})
		}
		web := "off"
		if x.bool("assistant.web") {
			web = "on"
		}
		row(c, "src.web", "web search", web, th.C(theme.FgMuted), func(c *ui.Ctx) {
			tone := th.C(theme.FgGhost)
			if web == "on" {
				tone = th.C(theme.OK)
			}
			c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
		})

		c.Box("gap2", ui.Style{H: ui.Px(6)}, nil)
		heading(c, "recent", "RECENT ACTIONS")
		n := 0
		for _, e := range slices.Backward(log) {
			if e.Kind != assistant.KindAction || n == 5 {
				continue
			}
			n++
			mark, tone := "✓", th.C(tAsstDone)
			if e.Undone {
				mark, tone = "↶", th.C(theme.FgFaint)
			}
			row(c, fmt.Sprint("act", e.Seq), e.Text, ago(now, e.At), th.C(theme.FgFaint), func(c *ui.Ctx) { c.Text("m", mark, mono(11, typeset.Bold, tone)) })
		}
		if n == 0 {
			c.Text("none", "nothing yet", monoFont(11, th.C(theme.FgGhost)))
		}
		c.Box("fill", ui.Style{H: ui.Flex(1)}, nil)
		wake := "wake word off"
		if on, _ := x.opt("voice.wake").(bool); on && x.bool("assistant.wake") {
			word, _ := x.opt("voice.wake_word").(string)
			wake = "wake word “" + word + "”"
		}
		spoken := "on"
		if s, _ := x.opt("voice.speech").(string); s == "off" {
			spoken = "off"
		}
		c.Row("voice", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("w", wake, monoFont(10.5, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("s", "spoken replies "+spoken, monoFont(10.5, th.C(theme.FgFaint)))
		})
	})
}

// lastRead is when each source was last read: the last time a tool reading it was used.
func (x *assistantPlugin) lastRead(log []assistant.Entry) map[string]time.Time {
	reads := map[string]string{}
	for _, t := range registry.AgentToolsFor(x.h, assistantAgent, true) {
		reads[t.Name] = t.Reads
	}
	out := map[string]time.Time{}
	for _, e := range log {
		if s := reads[e.Tool]; e.Kind == assistant.KindTool && s != "" {
			out[s] = e.At
		}
	}
	return out
}

// confirmBody is a confirmation's facts and its block, in the drawer's card and the inbox's.
func confirmBody(c *ui.Ctx, x *assistantPlugin, cf registry.Confirmation) {
	th := c.Theme()
	if len(cf.Rows) > 0 {
		c.Column("rows", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			for i, r := range cf.Rows {
				if r.Value == "" {
					continue
				}
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					c.Box("k", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) { c.Text("t", r.Key, monoFont(10.5, th.C(theme.FgFaint))) })
					if r.URL != "" {
						if widget.Link(c, "v", r.Value, assistantHelp+"#confirm") {
							x.report(x.sh.desktop.OpenURL(r.URL))
						}
						return
					}
					c.Text("v", r.Value, uiFont(12.5, th.C(theme.Fg)))
				})
			}
		})
	}
	if b := cf.Block; b != nil {
		c.Column("block", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
			c.Text("l", strings.ToUpper(b.Label), kickerFont(th.C(theme.FgGhost)))
			text := b.Text
			if len(b.Items) > 0 {
				text = "• " + strings.Join(b.Items, "\n• ")
			}
			widget.Wrap(c, "t", text, 560, uiFont(12.5, th.C(theme.FgMuted)))
		})
	}
}
