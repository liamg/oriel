package sessions

import (
	"cmp"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// fieldOpts are a launcher field's options; W defaults to the row's width and Lines to one.
type fieldOpts struct {
	Placeholder string
	Focus       bool // take the focus when the form asks for it (focusField)
	W           ui.Size
	Lines       int // above 1, a Prose of that many lines
}

// field is a text field of the launcher's: Tab moves on, Escape closes, ⌘↵ starts.
func (x *Launcher) field(c *ui.Ctx, key any, text *string, label string, o fieldOpts) {
	take := o.Focus && x.focusField // the mode's first field, as the form opens or its mode changes
	if take {
		x.focusField = false
	}
	if o.W == (ui.Size{}) {
		o.W = ui.Flex(1)
	}
	if o.Lines <= 1 {
		io := widget.InputOpts{Label: label, Placeholder: o.Placeholder, Help: "agent/launcher", TabMoves: true, KeyMode: LauncherKeyMode, Width: o.W,
			DefaultFocus: o.Focus, TakeFocus: take}
		widget.DialogField(c, key, text, io, x.close, nil)
		return
	}
	if take {
		c.RequestFocus(c.Next(key))
	}
	if widget.Prose(c, key, text, widget.ProseOpts{Label: label, Placeholder: o.Placeholder, Help: "agent/launcher", TabMoves: true,
		KeyMode: LauncherKeyMode, Width: o.W, Lines: o.Lines, MinLines: 2}).Cancelled {
		x.close()
	}
}

func (x *Launcher) form(c *ui.Ctx) {
	k, loaded := x.current()
	x.promptSection(c, startsFrom(k))
	x.modeSection(c, loaded)
	x.agentSection(c, k)
	c.Row("where", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
		if takesRepos(k) {
			c.Column("repos", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) { x.reposSection(c, k) })
		}
		c.Column("run", ui.Style{W: ui.Flex(1), Gap: 10}, x.runsIn)
	})
}

// promptSection is what the session starts from (a PR, a name, a directory) and the prompt, with its ticket.
func (x *Launcher) promptSection(c *ui.Ctx, f core.From) {
	th := c.Theme()
	c.Column("prompt", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		widget.Heading(c, "head", "Prompt", "⌘↵ starts · esc closes")
		switch f {
		case core.FromPR:
			x.field(c, "pr", &x.pr, "Pull request", fieldOpts{Placeholder: PRPlaceholder, Focus: true})
		case core.FromName:
			c.Row("repo", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "name", &x.name, "Name", fieldOpts{Placeholder: namePlaceholder, Focus: true})
				if t, ok := widget.Select(c, "template", "template: "+x.template, hostapi.Templates,
					widget.SelectOpts{Label: "Template", Help: "agent/launcher#prompt"}); ok {
					x.template = t
				}
				x.field(c, "parent", &x.parent, "Where it goes", fieldOpts{Placeholder: parentPlaceholder})
			})
		case core.FromDir:
			x.field(c, "dir", &x.dir, "Directory", fieldOpts{Placeholder: dirPlaceholder, Focus: true})
		}
		before := x.prompt
		x.field(c, "text", &x.prompt, "Prompt", fieldOpts{Placeholder: PromptPlaceholder, Focus: f == core.FromPrompt, Lines: 8})
		if f != core.FromPrompt {
			return
		}
		x.pastedPR(before)
		x.lookTicket(c)
		x.ticketCard(c)
		c.Row("extra", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			x.field(c, "ticket", &x.ticket, "Ticket", fieldOpts{Placeholder: "ticket: ENG-2231", W: ui.Px(220)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			note := "the ticket names the branch and goes to the agent"
			if x.ticketHere() != nil {
				note = "the ticket, comments and attachments go to the agent as context"
			}
			c.Text("note", note, widget.MonoFont(10, th.C(theme.FgFaint)))
		})
	})
}

// modeSection is the kinds of session to choose from.
func (x *Launcher) modeSection(c *ui.Ctx, loaded bool) {
	th := c.Theme()
	c.Column("mode", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		widget.Heading(c, "head", "Mode", "")
		if !loaded {
			c.Text("wait", cmp.Or(x.loadErr, "No kinds of session"), widget.UIFont(12, th.C(theme.Warn)))
			return
		}
		items := make([]widget.Choice, len(x.kinds))
		for i, k := range x.kinds {
			items[i] = widget.Choice{Title: k.Title, Desc: k.Desc}
		}
		if widget.Choices(c, "kinds", items, &x.kind, widget.ChoicesOpts{Label: "Mode", Help: "agent/launcher#mode"}) {
			x.focusField, x.err = true, ""
		}
	})
}

// agentSection is the agents installed to run it, or the session it continues.
func (x *Launcher) agentSection(c *ui.Ctx, k core.KindSpec) {
	c.Column("agent", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		widget.Heading(c, "head", "Agent", "provider · model · effort")
		if se, ok := x.continues(); ok {
			x.continuing(c, se)
			return
		}
		ps := x.providerList()
		widget.Async(c, "probe", x.providersState(), widget.AsyncOpts{Policy: widget.Gate, Label: "Checking which agents are installed",
			Retry: x.providers.Reload, Help: "agent/launcher#agent", Loading: func(c *ui.Ctx) { x.agentsLoading(c, k) }}, func(c *ui.Ctx) {
			c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
				for _, p := range ps {
					x.providerCard(c, p)
				}
				x.noAgentCard(c, k)
			})
		})
		if x.providersState().Phase == widget.Failed {
			c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10, Pad: ui.Insets{T: 10}}, func(c *ui.Ctx) { x.noAgentCard(c, k) })
		}
	})
}

// agentsLoading holds the agent cards' place while the installed agents are checked.
func (x *Launcher) agentsLoading(c *ui.Ctx, k core.KindSpec) {
	c.Column("loading", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			widget.Skeleton(c, "skeleton", widget.SkeletonOpts{Block: render.Point{X: 260, Y: 96}, Count: 2, Dir: ui.Row, Gap: 10,
				Label: "Agents", Help: "agent/launcher#agent"})
			x.noAgentCard(c, k)
		})
		widget.Busy(c, "busy", x.providersState(), widget.BusyOpts{Label: "Checking which agents are installed", Help: "agent/launcher#agent"})
	})
}
