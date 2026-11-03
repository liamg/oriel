package ship

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// prView is the pr view, a main tab per project; its tab names the PR it shows.
type prView struct{ x *Ship }

func (v prView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "pr", Fn: func(c *ui.Ctx) { v.x.prTab(c, vc.Instance) }}
}

// Title is "Raise PR · api" until the PR is raised, then "PR #790 · api".
func (v prView) Title(project string) string {
	se, p, ok := v.x.here(project)
	if !ok {
		return ""
	}
	s, _ := v.x.ship(se.ID, p)
	w := s.words()
	if s.PR == nil {
		return "Raise " + w.Short + " · " + p
	}
	return fmt.Sprintf("%s %s%d · %s", w.Short, w.Sigil, s.PR.Number, p)
}

// prTab is a project's PR in the Ship page's style: Raise PR's form until it's raised, then the PR's overview, with
// Edit PR's form on asking; at the right the gates and the ticket.
func (x *Ship) prTab(c *ui.Ctx, project string) {
	th := c.Theme()
	se, p, ok := x.here(project)
	c.Column("prtab", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
		if !ok {
			kit.EmptyNote(c, "Raise PR is a session's project's: open the session.")
			return
		}
		widget.Async(c, "load", x.loadState(c, se.ID, p), widget.AsyncOpts{Policy: widget.Block, Label: "Reading " + p, Retry: func() { x.refresh(se.ID, true) },
			Help: "git/ship#raise"}, func(c *ui.Ctx) {
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { x.prTabBody(c, se, p) })
		})
	})
}

// editPR puts a project's raised PR's tab on its form, or back on its overview, dropping what was typed.
func (x *Ship) editPR(id core.SessionID, project string, edit bool) {
	k := formKey(id, project)
	f := x.prs[k]
	if f == nil {
		f = &prForm{}
		x.prs[k] = f
	}
	if f.editing == edit {
		return
	}
	f.editing, f.edited, f.save, f.veto, f.tab = edit, false, autosave{}, "", 0
}

// prTabBody is the tab once the project's been read.
func (x *Ship) prTabBody(c *ui.Ctx, se core.SessionState, p string) {
	th := c.Theme()
	s, _ := x.ship(se.ID, p)
	f := x.prForm(se.ID, s)
	overview := s.PR != nil && !f.editing
	if overview {
		x.overviewHeader(c, se, s)
	} else {
		x.prHeader(c, se, s, f)
	}
	c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		if overview {
			width := float32(640)
			if r, ok := c.Rect(c.Next("overview")); ok {
				width = max(r.Dx()-36, 240)
			}
			c.Column("overview", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				x.overview(c, se, s, width)
			})
		} else {
			c.Column("form", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 8}, func(c *ui.Ctx) {
				x.prFields(c, se, s, f)
			})
		}
		c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
		c.Column("side", ui.Style{W: ui.Px(260), H: ui.Flex(1), Fill: th.C(kit.TShipSide), Pad: ui.PadXY(16, 16), Gap: 8, ScrollY: true}, func(c *ui.Ctx) {
			x.gatesList(c, se)
			x.previous(c, se, s.Project)
			x.ticket(c, se)
			c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
			widget.Wrap(c, "note", "Across projects, the merge order and \"raise all\" live in Ship.", 226, kit.UIFont(11, th.C(theme.FgFaint)))
		})
	})
}

func (x *Ship) prHeader(c *ui.Ctx, se core.SessionState, s shipRead, f *prForm) {
	th := c.Theme()
	edit, w := s.PR.Open(), s.words()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center, Fill: th.C(kit.TShipHead)}, func(c *ui.Ctx) {
		title := "RAISE " + strings.ToUpper(w.Short)
		if edit {
			title = fmt.Sprintf("EDIT %s %s%d", strings.ToUpper(w.Short), w.Sigil, s.PR.Number)
		}
		c.Text("h", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06},
			Color: th.C(theme.Fg)})
		c.Box("sq", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: kit.TokenColour(th, se.ProjectColour(s.Project), th.C(theme.Accent))}, nil)
		c.Text("p", s.Project, kit.Mono(12, typeset.SemiBold, th.C(theme.Fg)))
		c.Row("meta", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("m", fmt.Sprintf("%s → %s · %s", s.Branch, cmp.Or(s.Base, "?"), kit.Counted(s.Commits, "commit")), kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		step := func(step string, o widget.ButtonOpts) widget.ButtonOpts { return x.stepOpts(se.ID, s.Project, step, o) }
		if s.PR != nil && widget.Button(c, "cancel", "Cancel", step("", widget.ButtonOpts{Ghost: true, Help: "git/ship#raise"})) {
			x.run("ship.pr.open", registry.Args{"project": s.Project, "edit": false})
		}
		if edit {
			if widget.Button(c, "update", "Update "+w.Short, step("Updating", widget.ButtonOpts{Primary: true, Help: "git/ship#raise"})) {
				x.run("ship.raise", registry.Args{"project": s.Project})
			}
			return
		}
		if widget.Button(c, "save", "Save draft", step("Saving", widget.ButtonOpts{Help: "git/ship#raise"})) {
			x.run("ship.draft.save", registry.Args{"project": s.Project})
		}
		if widget.Button(c, "draft", "Raise as draft", step("Raising", widget.ButtonOpts{Help: "git/ship#raise"})) {
			x.run("ship.raise", registry.Args{"project": s.Project, "draft": true})
		}
		if widget.Button(c, "raise", "Push + raise "+w.Short, step("Raising", widget.ButtonOpts{Primary: true, Help: "git/ship#raise"})) {
			x.run("ship.raise", registry.Args{"project": s.Project})
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// overviewHeader is a raised PR's header: its number, project, state and branch, and what can be done with it.
func (x *Ship) overviewHeader(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	w := s.words()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center, Fill: th.C(kit.TShipHead)}, func(c *ui.Ctx) {
		c.Text("h", fmt.Sprintf("%s %s%d", strings.ToUpper(w.Short), w.Sigil, s.PR.Number),
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06}, Color: th.C(theme.Fg)})
		c.Box("sq", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: kit.TokenColour(th, se.ProjectColour(s.Project), th.C(theme.Accent))}, nil)
		c.Text("p", s.Project, kit.Mono(12, typeset.SemiBold, th.C(theme.Fg)))
		prStateChip(c, s.PR)
		c.Row("meta", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("m", fmt.Sprintf("%s → %s · %s", s.Branch, cmp.Or(s.Base, "?"), kit.Counted(s.Commits, "commit")), kit.MonoFont(11, th.C(theme.FgFaint)))
		})
		x.readyButton(c, se, s)
		x.mergeButton(c, se, s)
		x.prActions(c, se, s)
		switch {
		case s.PR.Open():
			if widget.Button(c, "edit", "Edit "+w.Short, x.stepOpts(se.ID, s.Project, "Updating", widget.ButtonOpts{Primary: true, Help: "git/ship#raise"})) {
				x.run("ship.pr.open", registry.Args{"project": s.Project, "edit": true})
			}
		default:
			ships, _ := x.sessionShips(se)
			finish := kit.AllMerged(se, projectShips(ships))
			x.nextButton(c, se, s, !finish)
			if finish && widget.Button(c, "finish", "Finish session", widget.ButtonOpts{Primary: true, Help: kit.EndHelp}) {
				x.run("session.end", registry.Args{"id": string(se.ID)})
			}
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// overview is a raised PR as its code host has it: the title, its link, reviewers and links to the session's other PRs,
// then its checks, its description and its review threads.
func (x *Ship) overview(c *ui.Ctx, se core.SessionState, s shipRead, width float32) {
	th := c.Theme()
	if e := x.failed(se.ID, s.Project); e != "" {
		widget.Wrap(c, "failed", e, width, kit.MonoFont(11, th.C(theme.Error)))
	}
	if s.HostError != "" {
		widget.Wrap(c, "gherr", s.HostError, width, kit.MonoFont(11, th.C(theme.Error)))
	}
	widget.Wrap(c, "title", s.PR.Title, width, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 18, Weight: typeset.SemiBold},
		Color: th.C(theme.Fg)})
	if s.PR.URL != "" {
		c.Row("link", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			if widget.Link(c, "url", s.PR.URL, "git/ship#pr") {
				x.run("ship.browse", registry.Args{"project": s.Project})
			}
			if widget.Button(c, "copy", "Copy link", widget.ButtonOpts{Ghost: true, Help: "git/ship#pr"}) {
				x.run("ship.copy-link", registry.Args{"project": s.Project})
			}
		})
	}
	x.reviewersLine(c, s, s.PR.Reviewers, s.Owners)
	x.links(c, se, s)
	section := func(key string, body func(c *ui.Ctx)) {
		c.Column(key, ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(tShipCard), Border: 1, Pad: ui.PadXY(14, 12), Gap: 8}, body)
	}
	inner := width - 30
	section("checks", func(c *ui.Ctx) {
		x.checks(c, s)
		if t, ok := ciTrouble(s); ok && s.PR.Open() {
			c.Row("ci", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				kit.AskTwinsLabelled(c, x.h, "ship.ci."+s.Project, t, "git/ship#checks", "Explain with agent", "Fix CI with agent")
			})
		}
		kit.ExplainKept(c, x.h, "ship.ci."+s.Project, "git/ship#checks")
	})
	section("description", func(c *ui.Ctx) {
		kit.SectionCap(c, "DESCRIPTION")
		if strings.TrimSpace(s.PR.Body) == "" {
			c.Text("none", "No description.", kit.UIFont(12, th.C(theme.FgFaint)))
			return
		}
		kit.MarkdownText(c, x.h, "md", s.PR.Body, inner, kit.UIFont(12.5, th.C(theme.FgMuted)))
	})
	section("review", func(c *ui.Ctx) { x.reviewThreads(c, se, s, inner) })
}

// prThreads are the session's threads mirrored from a project's PR: its review threads and its conversation.
func prThreads(se core.SessionState, s shipRead) []core.Thread {
	if s.PR == nil || s.Code == nil {
		return nil
	}
	var out []core.Thread
	for _, t := range se.Threads {
		if g := t.Hosted; g != nil && g.Project == s.Project && g.PR == s.PR.Number && cmp.Or(g.Host, "github") == s.Code.Name {
			out = append(out, t)
		}
	}
	return out
}

// reviewThreads is the PR's review, as mirrored into the session: each thread's place, reviewer and where they stand,
// and its comments.
func (x *Ship) reviewThreads(c *ui.Ctx, se core.SessionState, s shipRead, width float32) {
	th := c.Theme()
	ts := prThreads(se, s)
	if len(ts) == 0 {
		kit.SectionCap(c, "REVIEW · NONE YET")
		c.Text("none", "No review comments yet.", kit.UIFont(12, th.C(theme.FgFaint)))
		return
	}
	kit.SectionCap(c, "REVIEW · "+strings.ToUpper(kit.Counted(len(ts), "thread")))
	for _, t := range ts {
		place := "conversation"
		if a := t.Anchor; a != nil {
			place = fmt.Sprintf("%s:%d", a.File, a.Line)
		}
		c.Column(string(t.ID), ui.Style{W: ui.Flex(1), Gap: 6, Pad: ui.Insets{T: 8}, Role: ui.RoleGroup, Label: "Thread " + place}, func(c *ui.Ctx) {
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Row("place", ui.Style{Clip: true}, func(c *ui.Ctx) { c.Text("p", place, kit.MonoFont(11, th.C(theme.FgMuted))) })
				if r := t.Hosted.Reviewer; r != "" {
					widget.Chip(c, "who", "@"+r, th.C(tGitHub), "git/github#threads")
				}
				if word, tok := githubReviewWord(t.Hosted.State); word != "" {
					widget.Chip(c, "state", word, th.C(tok), "git/github#threads")
				}
				if !t.Open() {
					widget.Chip(c, "status", string(t.Status), th.C(theme.StatusQuiet), "git/github#threads")
				}
				if t.Hosted.Outdated {
					widget.Chip(c, "outdated", "outdated", th.C(theme.FgFaint), "git/github#threads")
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "open", "Open", widget.ButtonOpts{Ghost: true, Help: "git/github#threads"}) {
					x.run("request.open", registry.Args{"thread": string(t.ID)})
				}
			})
			for _, cm := range t.Comments {
				if cm.Draft {
					continue
				}
				c.Column(cm.ID, ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
					c.Row("by", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("n", cmp.Or(cm.Name, string(cm.Author)), kit.Mono(11, typeset.SemiBold, th.C(theme.Fg)))
						if !cm.Time.IsZero() {
							widget.Ago(c, "at", cm.Time, widget.AgoOpts{Text: kit.MonoFont(10, th.C(theme.FgFaint))})
						}
					})
					kit.MarkdownTextIn(c, x.h, "body", cm.Body, width, kit.UIFont(12.5, th.C(theme.FgMuted)), kit.ThreadMarkdown(se, t))
				})
			}
			if n := len(t.Drafts()); n > 0 {
				c.Text("drafts", fmt.Sprintf("%d %s to forward", n, kit.Plural(n, "draft reply", "draft replies")), kit.MonoFont(10.5, th.C(tGitHub)))
			}
		})
	}
}

// prFields is the tab's form: title, body (write or preview), reviewers and cross-linked PRs.
func (x *Ship) prFields(c *ui.Ctx, se core.SessionState, s shipRead, f *prForm) {
	th := c.Theme()
	if f.veto != "" {
		widget.Wrap(c, "veto", f.veto, 600, kit.MonoFont(11, th.C(theme.Warn)))
	}
	if e := x.failed(se.ID, s.Project); e != "" {
		widget.Wrap(c, "failed", e, 600, kit.MonoFont(11, th.C(theme.Error)))
	}
	kit.SectionCap(c, "TITLE")
	first := !f.edited
	typed := false
	if r := widget.Input(c, "title", &f.title, widget.InputOpts{Label: "Title", Placeholder: "feat(api): what it does", Help: "git/ship#raise"}); r.Changed {
		f.edited, typed = true, true
	}
	c.Box("sp", ui.Style{H: ui.Px(4)}, nil)
	c.Row("bodyhead", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		kit.SectionCap(c, "BODY")
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
				c.Text("file", s.TemplateFile, kit.MonoFont(10, th.C(theme.FgFaint)))
			}
			if widget.Button(c, "redraft", "Redraft", widget.ButtonOpts{Ghost: true, Help: "git/ship#drafts", Disabled: !x.h.Available("ship.redraft", nil)}) {
				x.run("ship.redraft", registry.Args{"project": s.Project, "kind": "pr"})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8)}, func(c *ui.Ctx) {
			if f.tab == 1 {
				kit.MarkdownText(c, x.h, "preview", f.body, max(200, c.Size().X-40), kit.UIFont(12.5, th.C(theme.FgMuted)))
				return
			}
			if r := widget.Prose(c, "body", &f.body, widget.ProseOpts{Label: "Body", Placeholder: "What and why", Lines: 18, MinLines: 10,
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
			kit.SectionCap(c, "REVIEWERS")
			x.reviewerRows(c, s, f)
		})
		c.Column("linked", ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(tShipCard), Border: 1, Pad: ui.PadXY(12, 10), Gap: 6}, func(c *ui.Ctx) {
			kit.SectionCap(c, "CROSS-LINKED PRS")
			if len(se.Projects) < 2 {
				c.Text("none", "One project: nothing to link.", kit.UIFont(12, th.C(theme.FgFaint)))
				return
			}
			x.links(c, se, s)
			c.Text("both", "both descriptions link · merged in the session's order", kit.MonoFont(10, th.C(theme.FgFaint)))
		})
	})
}

// reviewerRows are the PR's reviewers, each removable, and a field to add one: CODEOWNERS' first.
func (x *Ship) reviewerRows(c *ui.Ctx, s shipRead, f *prForm) {
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
			kit.ReviewerAvatar(c, r)
			c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("n", kit.ReviewerName(r), kit.UIFont(12.5, th.C(theme.FgMuted))) })
			note := "added"
			if slices.Contains(s.Owners, r) {
				note = "CODEOWNERS"
			}
			if s.PR != nil && slices.Contains(s.PR.Reviewers, r) {
				note = "asked"
			}
			c.Text("w", note, kit.MonoFont(10, th.C(theme.FgFaint)))
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
