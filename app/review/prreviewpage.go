//oriel:kind review

package review

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit/prui"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// page is the tab: the PR's head, then the review being written and the PR's overview, and at the right its status.
func (x *PRReview) page(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	src, ok := x.site(se)
	if !ok {
		kit.EmptyNote(c, "No code host has "+reviewPR(se)+": its plugin may be off.")
		return
	}
	d := x.detail(src, reviewPR(se))
	x.mu.Lock()
	full, failed, done, read := d.d, d.err, d.done, d.read
	x.mu.Unlock()
	p, _ := prProject(se)
	f := x.form(se.ID)
	if wake := x.saveSummary(se, f, c.Now()); !wake.IsZero() {
		c.WakeAt(wake)
	}
	prui.PRFrame(c, "frame", reviewedStatus(full), func(c *ui.Ctx) {
		x.pageHead(c, se, src, full)
		widget.Rule(c, "rule")
		x.pageBanner(c, se, f, full, p, done)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			width := float32(640)
			if r, ok := c.Rect(c.Next("main")); ok {
				width = max(r.Dx()-36, 240)
			}
			c.Column("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 14}, func(c *ui.Ctx) {
				if done && full.Request.Author != "" {
					c.Text("who", fmt.Sprintf("%s wants to merge %s → %s · %s  +%d −%d", full.Request.Author, cmp.Or(full.Head, "?"), cmp.Or(full.Base, "?"),
						kit.Counted(len(full.Files), "file"), full.Request.Adds, full.Request.Dels), kit.MonoFont(11, th.C(theme.FgMuted)))
				}
				x.progress(c, se)
				x.review(c, se, src, f, width, done && (full.State == "merged" || full.State == "closed"))
				x.sent(c, se, width)
				switch {
				case !done:
					widget.Busy(c, "asking", widget.LoadState{Phase: widget.Pending, Since: d.since}, widget.BusyOpts{Label: "Asking " + src.Label() + " for the pull request",
						Size: 11, Help: prReviewHelp})
				case failed != "":
					widget.Wrap(c, "err", failed, width, kit.MonoFont(11, th.C(theme.Error)))
				default:
					x.overview(c, full, width)
				}
			})
			widget.VRule(c, "divider")
			c.Column("side", ui.Style{W: ui.Px(260), H: ui.Flex(1), Fill: th.C(kit.TShipSide), Pad: ui.PadXY(16, 16), Gap: 10, ScrollY: true}, func(c *ui.Ctx) {
				var fields [][2]string
				if p.Commit != "" {
					fields = append(fields, [2]string{"checkout", kit.ShortSHA(p.Commit)})
				}
				prStatusBoxes(c, prStatus{full: full, done: done, read: read, host: src.Label(), fields: fields})
			})
		})
	})
	reviewKeys(c)
	if f.preview != "" {
		x.previewDialog(c, se, src, f)
	}
}

// reviewedStatus is where the reviewed PR stands, from its state and its reviewers'; none while its host hasn't said.
func reviewedStatus(full registry.ReviewDetail) prui.PRStatus {
	if full.State == "" {
		return prui.PRStatus{}
	}
	var reviews []string
	for _, r := range full.Reviewers {
		reviews = append(reviews, r.State)
	}
	return prui.PRStatusOf(full.State, false, full.Request.Draft, reviews)
}

// pageHead is the tab's top bar: the PR's ref and title, and its open and fetch buttons.
func (x *PRReview) pageHead(c *ui.Ctx, se core.SessionState, src registry.HostSite, full registry.ReviewDetail) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center, Fill: th.C(kit.TShipHead)}, func(c *ui.Ctx) {
		c.Text("h", "REVIEW", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .06}, Color: th.C(theme.Fg)})
		c.Text("ref", kit.Label(se), kit.Mono(12, typeset.SemiBold, th.C(theme.Fg)))
		if full.State != "" { // until its host says, it's no state at all
			prui.PRBadge(c, x.h, "state", reviewedStatus(full), prReviewHelp)
		}
		c.Row("title", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", full.Request.Title, kit.UIFont(13, th.C(theme.FgMuted)))
		})
		if widget.Button(c, "open", "Open in "+src.Label(), widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
			x.run("system.open", registry.Args{"url": reviewPR(se)})
		}
		x.mu.Lock()
		busy := x.fetching[se.ID]
		x.mu.Unlock()
		if widget.Button(c, "fetch", "Fetch latest", widget.ButtonOpts{Help: prReviewHelp, Busy: busy, BusyLabel: "Fetching"}) {
			x.run("review.pr.fetch", registry.Args{"id": string(se.ID)})
		}
	})
}

// pageBanner says when the PR has closed or moved on since the review read it.
func (x *PRReview) pageBanner(c *ui.Ctx, se core.SessionState, f *reviewForm, full registry.ReviewDetail, p core.Project, done bool) {
	switch {
	case done && (full.State == "merged" || full.State == "closed"):
		x.banner(c, fmt.Sprintf("This pull request was %s: there's nothing left to review.", full.State), "Archive this review", func() {
			x.run("session.end", registry.Args{"id": string(se.ID), "how": "archive"})
		})
	case f.moved.To != "":
		x.banner(c, fmt.Sprintf("Fetched %s since the review read it.", kit.Counted(f.moved.Commits, "new commit")), "Ask the agent to review them", func() {
			x.run("review.pr.recheck", registry.Args{"id": string(se.ID)})
		})
	case done && full.Commit != "" && p.Commit != "" && full.Commit != p.Commit:
		x.banner(c, "The author has pushed since this checkout.", "Fetch latest", func() {
			x.run("review.pr.fetch", registry.Args{"id": string(se.ID)})
		})
	}
}

// overview is the PR's description and changed files.
func (x *PRReview) overview(c *ui.Ctx, full registry.ReviewDetail, width float32) {
	th := c.Theme()
	prDescription(c, x.h, full, width-24)
	prActivity(c, x.h, full.Events, width)
	if len(full.Files) > 0 {
		reviewCard(c, "files", fmt.Sprintf("FILES  %d", len(full.Files)), ui.Flex(1), func(c *ui.Ctx) {
			for i, fc := range full.Files {
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("p", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", fc.Path, kit.MonoFont(11, th.C(theme.Fg))) })
					c.Text("a", fmt.Sprintf("+%d", fc.Adds), kit.MonoFont(11, th.C(theme.OK)))
					c.Text("d", fmt.Sprintf("−%d", fc.Dels), kit.MonoFont(11, th.C(theme.Error)))
				})
			}
		})
	}
}

// reviewKeys is the foot of the tab: the keys that act on the selected comment.
func reviewKeys(c *ui.Ctx) {
	th := c.Theme()
	c.Row("keys", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Stroke: th.C(theme.Line), Clip: true,
		Help: prReviewKeysHelp}, func(c *ui.Ctx) {
		for i, k := range [][2]string{{"j k", "move"}, {"y", "keep"}, {"x", "drop"}, {"1–5", "severity"}, {"e", "edit"}, {"r", "reword"},
			{"⏎", "show in diff"}, {"S", "summary"}, {"F1", "help"}} {
			kit.KeyHint(c, i, k[0], k[1])
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("on", "on the selected comment", kit.MonoFont(10, th.C(theme.FgGhost)))
	})
}

func (x *PRReview) banner(c *ui.Ctx, text, action string, do func()) {
	th := c.Theme()
	c.Row("banner", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 8), Gap: 10, Cross: ui.Center, Fill: th.C(theme.BgRaised)}, func(c *ui.Ctx) {
		c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", text, kit.UIFont(12.5, th.C(theme.Fg))) })
		if widget.Button(c, "do", action, widget.ButtonOpts{Primary: true, Help: prReviewHelp}) {
			do()
		}
	})
	widget.Rule(c, "rule2")
}

// progress is where the two reviewers are: the agent, and the AI review beside it.
func (x *PRReview) progress(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	agent, agentBusy := "agent: not started", false
	if t, ok := reviewRequest(se); ok {
		switch {
		case !reviewing(se, t):
			agent = "agent: done"
		case se.Agent.Working:
			agent, agentBusy = "agent: reviewing", true
		default:
			agent = "agent: waiting to start"
		}
	}
	ai, aiBusy := "AI review: not run", false
	if r, ok := se.LastAIReview(); ok {
		switch {
		case r.Running:
			ai, aiBusy = "AI review: running", true
		case r.Error != "":
			ai = "AI review: failed"
		default:
			ai = "AI review: done"
		}
	}
	c.Row("progress", ui.Style{W: ui.Flex(1), Gap: 14, Cross: ui.Center, Help: prReviewHelp}, func(c *ui.Ctx) {
		for i, s := range []struct {
			text string
			busy bool
		}{{agent, agentBusy}, {ai, aiBusy}} {
			c.Row(i, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				if s.busy {
					widget.Busy(c, "spin", widget.LoadState{Phase: widget.Pending, Since: se.Created}, widget.BusyOpts{Size: 10, Clock: -1, Help: prReviewHelp})
				}
				c.Text("t", s.text, kit.MonoFont(10.5, th.C(theme.FgMuted)))
			})
		}
	})
}

// review is the review being written: the comments to triage, each kept or dropped, reworded or re-rated, then the
// summary and the verdicts.
func (x *PRReview) review(c *ui.Ctx, se core.SessionState, src registry.HostSite, f *reviewForm, width float32, ended bool) {
	th := c.Theme()
	items := x.items(se)
	if !slices.ContainsFunc(items, func(it reviewItem) bool { return it.id == f.sel }) && len(items) > 0 {
		i := max(slices.IndexFunc(items, func(it reviewItem) bool { return it.pick.State == "" }), 0)
		f.sel = items[i].id
	}
	decided, kept := 0, 0
	for _, it := range items {
		if it.pick.State != "" {
			decided++
		}
		if it.kept() {
			kept++
		}
	}
	if f.body == f.auto || f.auto == "" && (f.body == "" || isAutoSummary(f.body)) {
		f.body = autoSummary(items)
		f.auto = f.body
	}
	reviewCard(c, "review", "YOUR REVIEW", ui.Flex(1), func(c *ui.Ctx) {
		c.Row("state", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				text := fmt.Sprintf("%s · %d of %d triaged · %d to post", kit.Counted(len(items), "comment"), decided, len(items), kept)
				if len(items) == 0 {
					text = "No comments yet: the agent's and the AI review's land here as they're made, and yours (gc in the diff)."
				}
				c.Text("t", text, kit.UIFont(12.5, th.C(theme.FgMuted)))
			})
			if t, ok := reviewRequest(se); ok && widget.Button(c, "req", "Open request", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
				x.run("request.open", registry.Args{"thread": string(t.ID)})
			}
		})
		for _, it := range items {
			x.itemRow(c, se, f, it, width)
		}
		widget.Rule(c, "rule")
		c.Row("summaryhead", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("t", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Text("t", "SUMMARY", kit.MonoFont(10, th.C(theme.FgFaint))) })
			asked := x.draft(se).SummaryAsk != ""
			if widget.Button(c, "draft", "Draft summary", widget.ButtonOpts{Ghost: true, Key: "S", Busy: asked, BusyLabel: "The agent is drafting it",
				Help: prReviewHelp}) {
				x.run("review.pr.summary", registry.Args{"id": string(se.ID)})
			}
		})
		widget.Prose(c, "summary", &f.body, widget.ProseOpts{Label: "Summary", Placeholder: "A summary for " + cmp.Or(se.Name, "the author") + "…",
			Lines: 6, MinLines: 3, Help: prReviewHelp})
		c.Row("verdicts", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
			c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				c.Text("n", fmt.Sprintf("%s on lines, posted to %s with your verdict", kit.Counted(kept, "comment"), src.Label()), kit.MonoFont(10.5, th.C(theme.FgFaint)))
			})
			empty := strings.TrimSpace(f.body) == "" && kept == 0
			suggested := suggestedVerdict(items)
			if src.Submit == nil || ended {
				why := src.Label() + " can't take a review from Oriel: post it there"
				if ended {
					why = "It's no longer open"
				}
				c.Text("why", why, kit.MonoFont(10.5, th.C(theme.Warn)))
				if widget.Button(c, "there", "Open in "+src.Label(), widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					x.run("system.open", registry.Args{"url": reviewPR(se)})
				}
				return
			}
			for _, v := range []struct{ verdict, label string }{
				{registry.VerdictComment, "Comment"}, {registry.VerdictChanges, "Request changes"}, {registry.VerdictApprove, "Approve"},
			} {
				o := widget.ButtonOpts{Ghost: v.verdict != suggested, Primary: v.verdict == suggested, Help: prReviewHelp,
					Disabled: empty && v.verdict != registry.VerdictApprove}
				if widget.Button(c, v.verdict, v.label+"…", o) {
					x.run("review.pr.preview", registry.Args{"id": string(se.ID), "verdict": v.verdict})
				}
			}
		})
	})
}

// itemRow is one comment to triage: kept or not, its severity, place and title, who found it; selected, its words
// as the author gets them, and what can be done to them.
func (x *PRReview) itemRow(c *ui.Ctx, se core.SessionState, f *reviewForm, it reviewItem, width float32) {
	th := c.Theme()
	sel := f.sel == it.id
	fill := render.Color{}
	if sel {
		fill = th.C(theme.BgRaised)
	}
	c.Column(it.id, ui.Style{W: ui.Flex(1), Gap: 6, Fill: fill, Radius: 6, Pad: ui.PadXY(6, 4), Role: ui.RoleGroup, Label: it.title}, func(c *ui.Ctx) {
		c.Row("line", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			st := widget.CheckOff
			if it.kept() {
				st = widget.CheckOn
			}
			if widget.Check(c, "tick", st, widget.CheckOpts{Label: "Include " + it.title, Help: prReviewHelp}) {
				state := core.PickKeep
				if it.kept() {
					state = core.PickDrop
				}
				x.run("review.pr.pick", registry.Args{"id": string(se.ID), "item": it.id, "state": state})
			}
			place := fmt.Sprintf("%s:%d", it.at.File, it.at.Line)
			if sel && f.focus {
				c.RequestFocus(c.Next("go"))
				f.focus = false
			}
			widget.Chip(c, "sev", strings.ToUpper(prui.SeverityShort(cmp.Or(it.severity(), "note"))), prui.SeverityTone(th, it.severity()), prReviewHelp)
			at := widget.Clickable(c, "at", ui.Style{Role: ui.RoleLink, Label: "Show " + place, Tooltip: "Show it in the diff", Help: prReviewKeysHelp},
				func(c *ui.Ctx) { c.Text("t", place, kit.MonoFont(11, th.C(theme.Accent))) })
			if at.Clicked {
				x.run("review.pr.show", registry.Args{"id": string(se.ID), "item": it.id})
			}
			in := widget.Clickable(c, "go", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Focusable: true, KeyMode: PRReviewKeyMode, Role: ui.RoleButton,
				Label: "Select " + place, Tooltip: "Select it; again to show it in the diff", Help: prReviewKeysHelp}, func(c *ui.Ctx) {
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					tone := theme.Fg
					if it.pick.State == core.PickDrop {
						tone = theme.FgFaint
					}
					c.Text("t", it.title, kit.UIFont(12.5, th.C(tone)))
				})
				for _, tag := range itemTags(it) {
					c.Text(tag, tag, kit.MonoFont(10, th.C(theme.FgFaint)))
				}
			})
			if in.Clicked {
				if sel {
					x.run("review.pr.show", registry.Args{"id": string(se.ID), "item": it.id})
				}
				f.sel = it.id
			}
			if widget.Button(c, "code", "Jump to code", widget.ButtonOpts{Ghost: true, Help: prReviewKeysHelp}) {
				x.run("review.pr.show", registry.Args{"id": string(se.ID), "item": it.id})
			}
			x.itemMenu(c, se, it)
		})
		if sel {
			x.itemDetail(c, se, f, it, width)
		}
	})
}

func itemTags(it reviewItem) []string {
	var tags []string
	switch {
	case it.mine:
		tags = append(tags, "you")
	case it.both():
		tags = append(tags, "agent + AI review")
	case it.finding != "":
		tags = append(tags, "AI review")
	}
	if it.pick.Body != "" {
		tags = append(tags, "reworded")
	}
	if it.pick.Severity != "" {
		tags = append(tags, "re-rated")
	}
	switch it.pick.State {
	case core.PickKeep:
		tags = append(tags, "kept")
	case core.PickDrop:
		tags = append(tags, "dropped")
	}
	return tags
}

// itemMenu is a comment's ⋯: keep or drop it, edit or rework its words, rate it, show it.
func (x *PRReview) itemMenu(c *ui.Ctx, se core.SessionState, it reviewItem) {
	type act struct {
		label, cmd string
		args       registry.Args
	}
	acts := []act{
		{"Keep it  y", "review.pr.pick", registry.Args{"state": core.PickKeep}},
		{"Drop it  x", "review.pr.pick", registry.Args{"state": core.PickDrop}},
		{"Edit its words  e", "review.pr.edit", nil},
		{"Ask: shorter  r", "review.pr.rework", registry.Args{"how": "shorter"}},
		{"Ask: friendlier", "review.pr.rework", registry.Args{"how": "friendlier"}},
		{"Ask: add a suggestion", "review.pr.rework", registry.Args{"how": "suggestion"}},
		{"Ask: more detail", "review.pr.rework", registry.Args{"how": "detail"}},
		{"Ask in your words…", "", nil},
	}
	for _, s := range core.Severities {
		acts = append(acts, act{"Rate it " + s, "review.pr.severity", registry.Args{"severity": s}})
	}
	acts = append(acts, act{"Show in the diff  ⏎", "review.pr.show", nil})
	items := make([]widget.MenuItem, len(acts))
	for i, a := range acts {
		items[i] = widget.MenuItem{Label: a.label, Help: prReviewHelp}
	}
	i := widget.MenuButton(c, "menu", items, widget.MenuButtonOpts{Label: "What to do with it", Glyph: "⋯", Width: 240, Help: prReviewHelp})
	if i < 0 {
		return
	}
	f := x.form(se.ID)
	f.sel = it.id
	if acts[i].cmd == "" {
		f.asking, f.ask = it.id, ""
		return
	}
	a := registry.Args{"id": string(se.ID), "item": it.id}
	for k, v := range acts[i].args {
		a[k] = v
	}
	x.run(acts[i].cmd, a)
}

// itemDetail is the selected comment opened: the words the author gets, or the field editing them, and the agent's
// rewording when one was asked for.
func (x *PRReview) itemDetail(c *ui.Ctx, se core.SessionState, f *reviewForm, it reviewItem, width float32) {
	th := c.Theme()
	c.Column("detail", ui.Style{W: ui.Flex(1), Gap: 8, Pad: ui.Insets{L: 30, R: 6, B: 6}}, func(c *ui.Ctx) {
		switch {
		case f.editing == it.id:
			widget.Prose(c, "edit", &f.edit, widget.ProseOpts{Label: "What the author gets", Lines: 8, MinLines: 3, Help: prReviewHelp})
			c.Row("btns", ui.Style{Gap: 8, Main: ui.End, W: ui.Flex(1)}, func(c *ui.Ctx) {
				if widget.Button(c, "reset", "The reviewer's words", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					f.edit = it.body
				}
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					f.editing = ""
				}
				if widget.Button(c, "save", "Save", widget.ButtonOpts{Primary: true, Help: prReviewHelp}) {
					x.run("review.pr.edit", registry.Args{"id": string(se.ID), "item": it.id, "body": f.edit})
				}
			})
		default:
			c.Text("cap", "WHAT THE AUTHOR GETS", kit.MonoFont(9.5, th.C(theme.FgFaint)))
			kit.MarkdownText(c, x.h, "words", it.words(), width-60, kit.UIFont(12.5, th.C(theme.Fg)), kit.CommentMarkdown())
		}
		if f.asking == it.id {
			widget.Input(c, "ask", &f.ask, widget.InputOpts{Label: "How should it change?", Placeholder: "How should the agent reword it?", EnterSubmits: true,
				Help: prReviewHelp})
			if widget.Button(c, "askit", "Ask the agent", widget.ButtonOpts{Primary: true, Disabled: strings.TrimSpace(f.ask) == "", Help: prReviewHelp}) {
				x.run("review.pr.rework", registry.Args{"id": string(se.ID), "item": it.id, "ask": f.ask})
			}
		}
		if it.pick.Rework == "" {
			return
		}
		text, ok := reworded(se, it.pick)
		if !ok {
			widget.Busy(c, "rework", widget.LoadState{Phase: widget.Pending, Since: se.Created}, widget.BusyOpts{Label: "The agent is rewording it", Size: 10,
				Clock: -1, Help: prReviewHelp})
			return
		}
		reviewCard(c, "reworded", "THE AGENT'S REWORDING", ui.Flex(1), func(c *ui.Ctx) {
			kit.MarkdownText(c, x.h, "t", text, width-84, kit.UIFont(12.5, th.C(theme.Fg)), kit.CommentMarkdown())
			c.Row("btns", ui.Style{Gap: 8, Main: ui.End, W: ui.Flex(1)}, func(c *ui.Ctx) {
				if widget.Button(c, "keep", "Keep mine", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					x.run("review.pr.rework.drop", registry.Args{"id": string(se.ID), "item": it.id})
				}
				if widget.Button(c, "use", "Use it", widget.ButtonOpts{Primary: true, Help: prReviewHelp}) {
					x.run("review.pr.rework.use", registry.Args{"id": string(se.ID), "item": it.id})
				}
			})
		})
	})
}

// sent is what the review has posted, and where each comment stands on the host now.
func (x *PRReview) sent(c *ui.Ctx, se core.SessionState, width float32) {
	items := sentItems(se)
	if reviewDraft(se) == nil || len(reviewDraft(se).Sent) == 0 {
		return
	}
	th := c.Theme()
	reviewCard(c, "sent", fmt.Sprintf("SENT  %d", len(items)), ui.Flex(1), func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Column("sent", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
				for i, r := range reviewDraft(se).Sent {
					widget.AgoLine(c, i, widget.AgoLineOpts{Text: kit.MonoFont(10.5, th.C(theme.FgMuted))},
						widget.AgoPart{Text: sentWord(r.Verdict)}, widget.AgoPart{Text: kit.Counted(r.Comments, "comment")}, widget.AgoPart{At: r.At})
				}
			})
			if widget.Button(c, "archive", "Archive this review", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
				x.run("session.end", registry.Args{"id": string(se.ID), "how": "archive"})
			}
		})
		for _, s := range items {
			c.Row(s.thread.ID, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				state := sentState(s)
				widget.Chip(c, "st", state, sentTone(th, state), prReviewHelp)
				place := fmt.Sprintf("%s:%d", s.thread.Anchor.File, s.thread.Anchor.Line)
				if widget.Link(c, "at", place, prReviewHelp) {
					if s.linked {
						x.run("request.open", registry.Args{"thread": string(s.thread.ID)})
					} else {
						x.run("diff.open", registry.Args{"path": reviewDir(se, s.thread.Anchor.Project) + "/" + s.thread.Anchor.File, "line": s.thread.Anchor.Line,
							"side": s.thread.Anchor.Side})
					}
				}
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", cmp.Or(s.thread.Title, firstWords(s.posted)), kit.UIFont(12.5, th.C(theme.Fg)))
				})
				if widget.Button(c, "code", "Jump to code", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					x.run("diff.open", registry.Args{"path": filepath.Join(reviewDir(se, s.thread.Anchor.Project), filepath.FromSlash(s.thread.Anchor.File)),
						"line": s.thread.Anchor.Line, "side": s.thread.Anchor.Side})
				}
			})
		}
	})
}

func sentTone(th *theme.Resolved, state string) render.Color {
	switch state {
	case "resolved":
		return th.C(theme.OK)
	case "author replied", "code changed":
		return th.C(theme.Accent)
	}
	return th.C(theme.FgMuted)
}

// previewDialog is the review as it will be posted: the verdict, the summary, and each comment as the author gets it.
func (x *PRReview) previewDialog(c *ui.Ctx, se core.SessionState, src registry.HostSite, f *reviewForm) {
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(680, size.X-32)
	items, comments := x.toPost(se)
	closed := widget.Scrim(c, "preview", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: prReviewHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if !c.Interaction(box).Focused && !f.focused {
			c.RequestFocus(box)
			f.focused = true
		}
		c.Column("box", ui.Style{W: ui.Px(w), H: ui.Px(min(640, size.Y-48)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: PRReviewPreviewKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}}, Role: ui.RoleDialog, Label: "Preview the review",
			Help: prReviewHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", strings.ToUpper(sentWord(f.preview)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold,
					Tracking: .24}, Color: th.C(theme.Fg)})
				c.Text("to", "on "+kit.Label(se)+", as you, on "+src.Label(), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					f.preview = ""
				}
			})
			widget.Rule(c, "rule")
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				c.Text("scap", "SUMMARY", kit.MonoFont(9.5, th.C(theme.FgFaint)))
				kit.MarkdownText(c, x.h, "summary", cmp.Or(strings.TrimSpace(f.body), "(none)"), w-48, kit.UIFont(12.5, th.C(theme.Fg)), kit.CommentMarkdown())
				for i, it := range items {
					c.Column(i, ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
						c.Text("at", fmt.Sprintf("%s:%d", it.at.File, it.at.Line), kit.MonoFont(10.5, th.C(theme.Accent)))
						kit.MarkdownText(c, x.h, "b", comments[i].Body, w-48, kit.UIFont(12.5, th.C(theme.Fg)), kit.CommentMarkdown())
					})
				}
				c.Text("note", "A comment off the diff's lines goes on its file; one on a file the PR doesn't change, in the summary.", kit.MonoFont(10, th.C(theme.FgFaint)))
			})
			widget.Rule(c, "rule2")
			// An approval's summary on a review that isn't one asks first: it says the opposite of the verdict.
			odd := f.preview != registry.VerdictApprove && strings.EqualFold(strings.TrimRight(strings.TrimSpace(f.body), ".!"), "LGTM")
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if odd {
					c.Row("odd", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
						c.Text("t", "Are you sure? The summary says LGTM, but this isn't an approval.", kit.UIFont(12, th.C(theme.Warn)))
					})
				}
				if widget.Button(c, "back", "Back", widget.ButtonOpts{Ghost: true, Help: prReviewHelp}) {
					f.preview = ""
				}
				label := "Post: " + sentWord(f.preview)
				if odd {
					label = "Post anyway"
				}
				if widget.Button(c, "post", label, widget.ButtonOpts{Primary: true, Help: prReviewHelp}) {
					x.run("review.pr.submit", registry.Args{"id": string(se.ID), "verdict": f.preview})
				}
			})
		})
	})
	if closed {
		f.preview = ""
	}
}

// foot is a review comment's place in the review, under its card in the diff and in Requests: whether it goes to the
// author, and keep, drop and rate it there and then; away from the code, a jump to it.
func (x *PRReview) foot(c *ui.Ctx, session, thread string, where uireg.ThreadWhere, width float32) {
	ref, ok := x.h.Session(core.SessionID(session))
	if !ok {
		return
	}
	se := ref.Snapshot()
	if reviewPR(se) == "" {
		return
	}
	th := c.Theme()
	if pk, ok := x.draft(se).Picks[thread]; ok && pk.Sent != "" {
		c.Row("review.sent", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Pad: ui.PadXY(0, 2)}, func(c *ui.Ctx) {
			c.Text("t", "Posted to the author in your review", kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
		return
	}
	var it reviewItem
	found := false
	for _, i := range x.items(se) {
		if string(i.thread) == thread || i.thread == "" && i.finding == thread {
			it, found = i, true
		}
	}
	if !found {
		return
	}
	a := func(extra registry.Args) registry.Args {
		out := registry.Args{"id": session, "item": it.id}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	c.Row("review", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Pad: ui.Insets{T: 6}, Stroke: th.C(theme.Line), Help: prReviewKeysHelp},
		func(c *ui.Ctx) {
			state := "goes to the author"
			switch {
			case it.pick.State == core.PickDrop:
				state = "dropped: not sent"
			case it.pick.State == "" && it.kept():
				state = "goes to the author unless you drop it"
			case it.pick.State == "":
				state = "not sent unless you keep it"
			}
			c.Text("cap", "YOUR REVIEW", kit.MonoFont(9.5, th.C(theme.FgFaint)))
			widget.Chip(c, "sev", strings.ToUpper(prui.SeverityShort(cmp.Or(it.severity(), "note"))), prui.SeverityTone(th, it.severity()), prReviewKeysHelp)
			c.Row("state", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", state, kit.MonoFont(10.5, th.C(theme.FgMuted))) })
			if it.pick.State != core.PickKeep && widget.Button(c, "keep", "Keep", widget.ButtonOpts{Ghost: true, Help: prReviewKeysHelp}) {
				x.run("review.pr.pick", a(registry.Args{"state": core.PickKeep}))
			}
			if it.pick.State != core.PickDrop && widget.Button(c, "drop", "Drop", widget.ButtonOpts{Ghost: true, Help: prReviewKeysHelp}) {
				x.run("review.pr.pick", a(registry.Args{"state": core.PickDrop}))
			}
			if widget.Button(c, "tab", "In the review ↗", widget.ButtonOpts{Ghost: true, Help: prReviewKeysHelp}) {
				x.form(se.ID).sel = it.id
				x.run("ship.pr.open", nil)
			}
			if where != uireg.ThreadInCode && widget.Button(c, "code", "Jump to code", widget.ButtonOpts{Ghost: true, Help: prReviewKeysHelp}) {
				x.run("review.pr.show", a(nil))
			}
		})
}
