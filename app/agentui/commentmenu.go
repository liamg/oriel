package agentui

import (
	"context"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// commentMenu is a comment's "⋯", shown while the pointer is over it: copy its text as it reads, copy its markdown,
// or read the markdown.
// A message of yours the agent hasn't had yet can be edited or deleted too.
func commentMenu(c *ui.Ctx, h plugin.Host, t core.Thread, cm core.Comment, shown bool) {
	items := []widget.MenuItem{
		{Label: "Copy", Help: "agent/requests#comments"},
		{Label: "Copy as markdown", Help: "agent/requests#comments"},
		{Label: "View markdown", Help: "agent/requests#comments"},
	}
	queued := t.Open() && cm.Author == core.AuthorYou && !cm.Delivered && !cm.Btw
	if queued {
		items = append(items, widget.MenuItem{Label: "Edit", Help: "agent/requests#comments"}, widget.MenuItem{Label: "Delete", Help: "agent/requests#comments"})
	}
	ref := registry.Args{"thread": string(t.ID), "comment": cm.ID}
	switch widget.MenuButton(c, "more", items, widget.MenuButtonOpts{Label: "Comment actions", Glyph: "⋯", Size: 18, Width: 200,
		Help: "agent/requests#comments", Hidden: !shown}) {
	case 3:
		runOrSay(h, "request.edit-queued", ref)
	case 4:
		runOrSay(h, "request.delete-queued", ref)
	case 0:
		kit.CopyText(h, kit.PlainText(cm.Body))
	case 1:
		kit.CopyText(h, cm.Body)
	case 2:
		if err := h.Run(context.Background(), "comment.markdown", registry.Args{"title": cmTitle(h, cm), "body": cm.Body}); err != nil {
			h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}
}

func runOrSay(h plugin.Host, cmd string, a registry.Args) {
	if err := h.Run(context.Background(), cmd, a); err != nil {
		h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
	}
}

func cmTitle(h plugin.Host, cm core.Comment) string {
	if cm.Author == core.AuthorAgent {
		return registry.ProviderTitle(h, cm.Name)
	}
	if cm.Name != "" {
		return cm.Name
	}
	return "you"
}

// mdViewer is the dialog comment.markdown opens: a comment's markdown as written.
type mdViewer struct {
	title, body string
	open        bool
	scroll      kit.Scroll
}

func (x *Requests) mdViewer() uireg.View {
	return uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "comment-markdown", Fn: func(c *ui.Ctx) {
			v := &x.md
			if !v.open {
				return
			}
			th := c.Theme()
			size := c.Size()
			centre := render.Point{X: size.X / 2, Y: size.Y / 2}
			w := min(760, size.X-32)
			if widget.Scrim(c, "md", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: "agent/requests#comments"}, func(c *ui.Ctx) {
				box := c.Next("box")
				for _, k := range c.Interaction(box).Keys {
					if k.Key == "Esc" {
						v.open = false
					}
				}
				c.Column("box", ui.Style{Focusable: true, DefaultFocus: true, KeyMode: widget.ScrimKeyMode, W: ui.Px(w), H: ui.Px(min(620, size.Y-48)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1,
					Radius: 10, Clip: true, Trap: true, Interactive: true, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
					Role: ui.RoleDialog, Label: "Markdown", Help: "agent/requests#comments"}, func(c *ui.Ctx) {
					c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(48), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", "MARKDOWN · "+strings.ToUpper(v.title), kit.MonoFont(11, th.C(theme.FgMuted)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if widget.Button(c, "copy", "Copy", widget.ButtonOpts{Ghost: true, Help: "agent/requests#comments"}) {
							kit.CopyText(x.h, v.body)
						}
						if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close", Help: "agent/requests#comments"}) {
							v.open = false
						}
					})
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					kit.Scrolled(c, "src", &v.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, ui.Style{Pad: ui.Pad(18), Gap: 2},
						func(c *ui.Ctx, _ func(string) ui.ID) {
							for i, ln := range strings.Split(v.body, "\n") {
								widget.Wrap(c, i, kit.ExpandTabs(ln), w-40, kit.MonoFont(12, th.C(theme.Fg)))
							}
						})
				})
			}) {
				v.open = false
			}
		}}
	})
}
