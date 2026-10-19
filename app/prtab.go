package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// prTab is Raise PR (or Edit PR once it's raised) for a project, a main tab in 18's style (42's panel 6): the title,
// the body from the agent's draft or the template, reviewers, cross-linked PRs; at the right the gates and the ticket.
func (x *shipPlugin) prTab(c *ui.Ctx, project string) {
	th := c.Theme()
	se, p, ok := x.here(project)
	c.Column("prtab", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
		if !ok {
			shipEmpty(c, "Raise PR is a session's project's: open the session.")
			return
		}
		s, read := x.ship(se.ID, p)
		if !read {
			shipEmpty(c, "Reading "+p+"…")
			return
		}
		f := x.prForm(se.ID, s)
		x.prHeader(c, se, s, f)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("form", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 8}, func(c *ui.Ctx) {
				x.prFields(c, se, s, f)
			})
			c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("side", ui.Style{W: ui.Px(260), H: ui.Flex(1), Fill: th.C(tShipSide), Pad: ui.PadXY(16, 16), Gap: 8, ScrollY: true}, func(c *ui.Ctx) {
				x.gatesList(c, se)
				x.ticket(c, se)
				c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
				widget.Wrap(c, "note", "Across projects, the merge order and \"raise all\" live in Ship.", 226, uiFont(11, th.C(theme.FgFaint)))
			})
		})
	})
}

func (x *shipPlugin) prHeader(c *ui.Ctx, se core.SessionState, s shipRead, f *prForm) {
	th := c.Theme()
	edit, w := s.PR.Open(), s.words()
	busy := x.doing(se.ID, s.Project)
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center, Fill: th.C(tShipHead)}, func(c *ui.Ctx) {
		title := "RAISE " + strings.ToUpper(w.Short)
		if edit {
			title = fmt.Sprintf("EDIT %s %s%d", strings.ToUpper(w.Short), w.Sigil, s.PR.Number)
		}
		c.Text("h", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06},
			Color: th.C(theme.Fg)})
		c.Box("sq", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: tokenColour(th, se.ProjectColour(s.Project), th.C(theme.Accent))}, nil)
		c.Text("p", s.Project, mono(12, typeset.SemiBold, th.C(theme.Fg)))
		add, del := 0, 0
		for _, fl := range s.Files {
			add, del = add+fl.Add, del+fl.Del
		}
		c.Row("meta", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("m", fmt.Sprintf("%s → %s · %s", s.Branch, cmpOr(s.Base, "?"), counted(s.Commits, "commit")), monoFont(11, th.C(theme.FgFaint)))
		})
		if busy != "" {
			c.Text("busy", busy, monoFont(11, th.C(tShipTodo)))
		}
		if widget.Button(c, "save", "Save draft", widget.ButtonOpts{Help: "git/ship#raise", Disabled: busy != ""}) {
			x.run("ship.draft.save", registry.Args{"project": s.Project})
		}
		if edit {
			if widget.Button(c, "update", "Update "+w.Short, widget.ButtonOpts{Primary: true, Help: "git/ship#raise", Disabled: busy != ""}) {
				x.run("ship.raise", registry.Args{"project": s.Project})
			}
			return
		}
		if widget.Button(c, "draft", "Raise as draft", widget.ButtonOpts{Help: "git/ship#raise", Disabled: busy != ""}) {
			x.run("ship.raise", registry.Args{"project": s.Project, "draft": true})
		}
		if widget.Button(c, "raise", "Push + raise "+w.Short, widget.ButtonOpts{Primary: true, Help: "git/ship#raise", Disabled: busy != ""}) {
			x.run("ship.raise", registry.Args{"project": s.Project})
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// prFields is the tab's form: title, body (write or preview), reviewers and cross-linked PRs.
func (x *shipPlugin) prFields(c *ui.Ctx, se core.SessionState, s shipRead, f *prForm) {
	th := c.Theme()
	if f.veto != "" {
		widget.Wrap(c, "veto", f.veto, 600, monoFont(11, th.C(theme.Warn)))
	}
	if e := x.failed(se.ID, s.Project); e != "" {
		widget.Wrap(c, "failed", e, 600, monoFont(11, th.C(theme.Error)))
	}
	shipCap(c, "TITLE")
	first := !f.edited
	typed := false
	if r := widget.Input(c, "title", &f.title, widget.InputOpts{Label: "Title", Placeholder: "feat(api): what it does", Help: "git/ship#raise"}); r.Changed {
		f.edited, typed = true, true
	}
	c.Box("sp", ui.Style{H: ui.Px(4)}, nil)
	c.Row("bodyhead", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		shipCap(c, "BODY")
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if !f.edited && !s.PR.Open() && !s.PRDraft.Empty() && s.PRDraft.By != "" {
			draftChip(c, s.PRDraft)
		}
		if s.Template != "" {
			widget.Chip(c, "tmpl", "template filled", th.C(theme.FgMuted), "git/ship#raise")
		}
	})
	c.Column("bodybox", ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(tShipCard), Border: 1, Clip: true}, func(c *ui.Ctx) {
		c.Row("tabs", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 6), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Segmented(c, "mode", []string{"WRITE", "PREVIEW"}, &f.tab, widget.SegmentedOpts{Label: "Body", Help: "git/ship#raise"})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if s.TemplateFile != "" {
				c.Text("file", s.TemplateFile, monoFont(10, th.C(theme.FgFaint)))
			}
			if widget.Button(c, "redraft", "Redraft", widget.ButtonOpts{Ghost: true, Help: "git/ship#drafts", Disabled: !x.h.Available("ship.redraft", nil)}) {
				x.run("ship.redraft", registry.Args{"project": s.Project, "kind": "pr"})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8)}, func(c *ui.Ctx) {
			if f.tab == 1 {
				markdownText(c, "preview", f.body, max(200, c.Size().X-40), uiFont(12.5, th.C(theme.FgMuted)))
				return
			}
			if r := widget.Input(c, "body", &f.body, widget.InputOpts{Label: "Body", Placeholder: "What and why", Lines: 18, MinLines: 10,
				Help: "git/ship#raise"}); r.Changed {
				f.edited, typed = true, true
			}
		})
	})
	if !s.PR.Open() {
		x.keepTyped(c, &f.save, typed, typed && first, host.DraftReq{Session: se.ID, Project: s.Project, Kind: "pr", Title: f.title, Body: f.body})
	}
	c.Box("sp2", ui.Style{H: ui.Px(6)}, nil)
	c.Row("boxes", ui.Style{W: ui.Flex(1), Gap: 14}, func(c *ui.Ctx) {
		c.Column("reviewers", ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(tShipCard), Border: 1, Pad: ui.PadXY(12, 10), Gap: 6}, func(c *ui.Ctx) {
			shipCap(c, "REVIEWERS")
			x.reviewerRows(c, s, f)
		})
		c.Column("linked", ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(tShipCard), Border: 1, Pad: ui.PadXY(12, 10), Gap: 6}, func(c *ui.Ctx) {
			shipCap(c, "CROSS-LINKED PRS")
			if len(se.Projects) < 2 {
				c.Text("none", "One project: nothing to link.", uiFont(12, th.C(theme.FgFaint)))
				return
			}
			x.links(c, se, s)
			c.Text("both", "both descriptions link · merged in the session's order", monoFont(10, th.C(theme.FgFaint)))
		})
	})
}

// reviewerRows are the PR's reviewers, each removable, and a field to add one: CODEOWNERS' first.
func (x *shipPlugin) reviewerRows(c *ui.Ctx, s shipRead, f *prForm) {
	th := c.Theme()
	rs := f.reviewersOf(s)
	if s.PR != nil {
		for _, r := range s.PR.Reviewers {
			if !slices.Contains(rs, r) {
				rs = append(rs, r)
			}
		}
	}
	for _, r := range rs {
		c.Row(r, ui.Style{W: ui.Flex(1), H: ui.Px(24), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			reviewerAvatar(c, r)
			c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("n", shortName(r), uiFont(12.5, th.C(theme.FgMuted))) })
			note := "added"
			if slices.Contains(s.Owners, r) {
				note = "CODEOWNERS"
			}
			if s.PR != nil && slices.Contains(s.PR.Reviewers, r) {
				note = "asked"
			}
			c.Text("w", note, monoFont(10, th.C(theme.FgFaint)))
			if widget.IconButton(c, "rm", "✕", widget.IconButtonOpts{Label: "Remove " + r, Help: "git/ship#raise", Size: 18}) {
				f.removed = append(f.removed, r)
				f.reviewers = remove(f.reviewers, r)
			}
		})
	}
	if r := widget.Input(c, "add", &f.adding, widget.InputOpts{Label: "Add a reviewer", Placeholder: "+ a login or org/team, ⏎", Help: "git/ship#raise"}); r.Submitted {
		if who := strings.TrimPrefix(strings.TrimSpace(f.adding), "@"); who != "" {
			f.reviewers = append(f.reviewers, who)
			f.removed = remove(f.removed, who)
		}
		f.adding = ""
	}
}

func remove(xs []string, s string) []string {
	return slices.DeleteFunc(slices.Clone(xs), func(x string) bool { return x == s })
}
