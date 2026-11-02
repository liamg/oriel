package ship

import (
	"cmp"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// prStep is the PR: not raised, with its draft and reviewers; or raised, with its checks.
func (x *Ship) prStep(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	w := s.words()
	if s.Code == nil || s.Unkeyed {
		note := "no remote"
		switch {
		case s.Unkeyed:
			note = "needs your " + s.Code.Label() + " token for " + s.Site.Host
		case s.Unknown != "":
			note = s.Unknown + " isn't a code host Oriel knows yet"
		case s.Remote:
			note = "origin isn't on a code host Oriel knows"
		}
		shipCard(c, false, func(c *ui.Ctx) {
			stepTitle(c, sentence(w.Long), note)
			if (s.Unkeyed || s.Unknown != "") && widget.Button(c, "setup", "Set up…", widget.ButtonOpts{Help: codeSetupHelp}) {
				x.run("code.setup", setupArgs(s))
			}
		}, nil)
		return
	}
	if s.PR == nil {
		f := x.prForm(se.ID, s)
		note := "not raised"
		switch {
		case f.edited:
			note += " · draft ready"
		case !s.PRDraft.Empty() && s.PRDraft.Stale:
			note += " · draft stale"
		case !s.PRDraft.Empty():
			note += " · draft ready"
		}
		shipCard(c, false, func(c *ui.Ctx) {
			stepTitle(c, sentence(w.Long), note)
			if s.Template != "" {
				widget.Chip(c, "tmpl", "template filled", th.C(tShipTodo), "git/ship#raise")
			}
		}, func(c *ui.Ctx) {
			x.reviewersLine(c, s, f.reviewersOf(s), s.Owners)
			x.links(c, se, s)
			c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				if widget.Button(c, "raise", "Raise "+w.Short+"…", widget.ButtonOpts{Help: "git/ship#raise"}) {
					x.run("ship.pr.open", registry.Args{"project": s.Project})
				}
			})
			if s.HostError != "" {
				widget.Wrap(c, "gherr", s.HostError, 420, kit.MonoFont(10.5, th.C(theme.Error)))
			}
		})
		return
	}
	shipCard(c, s.PR.Open() && ciOf(s.Checks) != registry.CheckOK, func(c *ui.Ctx) {
		c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", fmt.Sprintf("%s%d · %s", w.Sigil, s.PR.Number, s.PR.Title),
				ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		})
		prStateChip(c, s.PR)
	}, func(c *ui.Ctx) {
		x.checks(c, s)
		c.Row("fresh", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { checksFoot(c, s) })
		x.reviewersLine(c, s, s.PR.Reviewers, s.Owners)
		x.links(c, se, s)
		c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			x.readyButton(c, se, s)
			if s.PR.Open() && widget.Button(c, "edit", "Edit "+w.Short, widget.ButtonOpts{Help: "git/ship#raise"}) {
				x.run("ship.pr.open", registry.Args{"project": s.Project, "edit": true})
			}
			x.mergeButton(c, se, s)
			x.prActions(c, se, s)
			if !s.PR.Open() {
				x.nextButton(c, se, s, false)
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "browse", "Open on "+s.Code.Label()+" ↗", widget.ButtonOpts{Help: "git/ship#pr"}) {
				x.run("ship.browse", registry.Args{"project": s.Project})
			}
		})
		if t, ok := ciTrouble(s); ok {
			c.Row("ci", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				kit.AskTwinsLabelled(c, x.h, "ship.ci."+s.Project, t, "git/ship#checks", "Explain with agent", "Fix CI with agent")
			})
		}
		kit.ExplainKept(c, x.h, "ship.ci."+s.Project, "git/ship#checks")
	})
}

// nextButton is a project's next step when it has no open PR: the next PR once its PR's done with and nothing's
// left on its branch, else raising one.
func (x *Ship) nextButton(c *ui.Ctx, se core.SessionState, s shipRead, primary bool) {
	w := s.words()
	switch {
	case movesOn(s) && registry.Can(x.h, se.Kind, core.NextPR):
		if widget.Button(c, "next", "Start next "+w.Short, x.stepOpts(se.ID, s.Project, nextPRStep, widget.ButtonOpts{Primary: primary, Help: "git/ship#next"})) {
			x.run("ship.next-pr", registry.Args{"project": s.Project})
		}
	case s.Code == nil:
	case s.PR == nil:
		if widget.Button(c, "raise", "Raise "+w.Short, widget.ButtonOpts{Primary: primary, Help: "git/ship#raise"}) {
			x.run("ship.pr.open", registry.Args{"project": s.Project})
		}
	default:
		if widget.Button(c, "again", "Raise a new "+w.Short+"…", widget.ButtonOpts{Primary: primary, Help: "git/ship#raise"}) {
			x.run("ship.pr.open", registry.Args{"project": s.Project, "edit": true})
		}
	}
}

// prStateChip is where a PR stands: open, draft, merged or closed.
func prStateChip(c *ui.Ctx, pr *registry.PR) {
	widget.Chip(c, "state", prStateWord(pr), prStateTone(c, prStateWord(pr)), "git/ship#pr")
}

// prStateWord is where a PR stands, in a word: open, draft, merged or closed.
func prStateWord(pr *registry.PR) string {
	switch {
	case pr.Merged:
		return "merged"
	case pr.State == "closed":
		return "closed"
	case pr.Draft:
		return "draft"
	}
	return "open"
}

func prStateTone(c *ui.Ctx, word string) color.NRGBA {
	th := c.Theme()
	switch word {
	case "merged":
		return th.C(theme.StatusPermission)
	case "closed":
		return th.C(theme.FgFaint)
	case "draft", "not raised":
		return th.C(theme.FgMuted)
	case "drafting":
		return th.C(tShipTodo)
	}
	return th.C(theme.OK)
}

// readyButton is Ready for review, on a draft PR.
func (x *Ship) readyButton(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR.Open() && s.PR.Draft && widget.Button(c, "ready", "Ready for review", x.stepOpts(se.ID, s.Project, "Marking ready", widget.ButtonOpts{Help: "git/ship#pr"})) {
		x.run("ship.ready", registry.Args{"project": s.Project})
	}
}

// mergeButton is Merge…, on an open PR whose code host merges.
func (x *Ship) mergeButton(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR.Open() && s.Code != nil && s.Code.Merge != nil && widget.Button(c, "merge", "Merge…", x.stepOpts(se.ID, s.Project, "Merging", widget.ButtonOpts{Help: mergeHelp})) {
		x.run("ship.merge", registry.Args{"project": s.Project})
	}
}

// prActions are the buttons plugins add to a raised PR (registry.PRAction).
func (x *Ship) prActions(c *ui.Ctx, se core.SessionState, s shipRead) {
	if s.PR == nil {
		return
	}
	pr := registry.PullRequest{Session: string(se.ID), Project: s.Project, Repo: s.Repo, Number: s.PR.Number, Title: s.PR.Title,
		URL: s.PR.URL, State: s.PR.State, Draft: s.PR.Draft, Merged: s.PR.Merged}
	for _, a := range registry.PRActions(x.h, pr) {
		args := a.RunArgs()
		if widget.Button(c, "action."+a.Name, a.Label, widget.ButtonOpts{Disabled: !x.h.Available(a.Command, args), Help: cmp.Or(a.Help, "git/ship#pr")}) {
			x.run(a.Command, args)
		}
	}
}

// checks lists a PR's checks, with what the first failing one's log says.
func (x *Ship) checks(c *ui.Ctx, s shipRead) {
	th := c.Theme()
	pass := 0
	for _, ch := range s.Checks {
		if ch.State == registry.CheckOK || ch.State == registry.CheckSkipped {
			pass++
		}
	}
	if len(s.Checks) == 0 {
		kit.SectionCap(c, "CHECKS · NONE YET")
		return
	}
	kit.SectionCap(c, fmt.Sprintf("CHECKS · %d OF %d", pass, len(s.Checks)))
	now := c.Now()
	for i, ch := range s.Checks {
		checkRow(c, i, ch, now)
	}
	if s.FailLine != "" {
		c.Row("fail", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 22}, Clip: true}, func(c *ui.Ctx) {
			c.Text("f", s.FailLine, kit.MonoFont(10.5, th.C(theme.Error)))
		})
	}
}

// checkRow is a check: its state's glyph, its name, and how long it took or has run.
func checkRow(c *ui.Ctx, key any, ch registry.Check, now time.Time) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		took := ""
		if d := ch.Took(now); d > 0 {
			took = kit.Duration(d)
		}
		glyph, tone, note := "✓", th.C(theme.OK), took
		switch ch.State {
		case registry.CheckFailed:
			glyph, tone = "✗", th.C(theme.Error)
		case registry.CheckSkipped:
			glyph, tone = "–", th.C(theme.FgFaint)
		case registry.CheckRunning, registry.CheckQueued:
			glyph, tone = "◌", th.C(tShipCI)
			note = "queued"
			if ch.State == registry.CheckRunning {
				note = strings.TrimSpace("running " + took)
			}
		}
		c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(12, tone)) })
		c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("n", ch.Name, kit.UIFont(12, th.C(theme.FgMuted))) })
		c.Text("d", note, kit.MonoFont(10.5, th.C(theme.FgFaint)))
	})
}

// reviewersLine is who reviews: each with their kit.Initials, CODEOWNERS' marked.
func (x *Ship) reviewersLine(c *ui.Ctx, s shipRead, reviewers, owners []string) {
	th := c.Theme()
	c.Row("reviewers", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		kit.SectionCap(c, "REVIEWERS")
		if len(reviewers) == 0 {
			c.Text("none", "none yet", kit.UIFont(12, th.C(theme.FgFaint)))
			return
		}
		for _, r := range reviewers {
			c.Row(r, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				reviewerAvatar(c, r)
				c.Text("n", shortName(r), kit.UIFont(12, th.C(theme.FgMuted)))
			})
		}
		if len(owners) > 0 && len(slices.DeleteFunc(slices.Clone(reviewers), func(r string) bool { return !slices.Contains(owners, r) })) > 0 {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("co", "CODEOWNERS", kit.MonoFont(10, th.C(theme.FgFaint)))
		}
	})
}

// shortName is a reviewer as shown: a team without its org.
func shortName(r string) string {
	if _, t, ok := strings.Cut(r, "/"); ok {
		return t
	}
	return r
}

// reviewerAvatar is a reviewer's kit.Initials on a tint.
func reviewerAvatar(c *ui.Ctx, who string) {
	th := c.Theme()
	tone := th.C(tShipLink)
	if !strings.Contains(who, "/") {
		tone = th.C(tShipTodo)
	}
	c.Box("av", ui.Style{W: ui.Px(17), H: ui.Px(17), Radius: 4, Fill: tone, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("i", kit.ProjectInitials(shortName(who)), kit.Mono(8, typeset.Bold, th.C(theme.AccentInk)))
	})
}

// links is how the project's PR relates to the session's others: what it depends on, or pairs with, and the order.
func (x *Ship) links(c *ui.Ctx, se core.SessionState, s shipRead) {
	if len(se.Projects) < 2 {
		return
	}
	th := c.Theme()
	ships, _ := x.sessionShips(se)
	var order, before, after []string
	seen := false
	for _, o := range ships {
		if o.ReadOnly || o.Code == nil {
			continue
		}
		order = append(order, o.Project)
		switch {
		case o.Project == s.Project:
			seen = true
		case !seen:
			before = append(before, prRef(o))
		default:
			after = append(after, o.Project)
		}
	}
	if len(order) < 2 {
		return
	}
	var text string
	switch {
	case len(before) > 0:
		text = "depends on " + strings.Join(before, ", ")
	case s.PR == nil:
		text = "pairs with " + strings.Join(after, ", ") + " → will cross-link both descriptions"
	default:
		text = "merges before " + strings.Join(after, ", ")
	}
	c.Row("links", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 7), Gap: 8, Radius: 5, Stroke: th.C(tShipCard), Border: 1, Cross: ui.Center, Clip: true},
		func(c *ui.Ctx) {
			c.Text("i", "⟷", kit.UIFont(12, th.C(tShipLink)))
			c.Text("t", text+" · merge order: "+strings.Join(order, " → "), kit.MonoFont(10.5, th.C(theme.FgMuted)))
		})
}

// prRef is a project's PR as another names it: "api #790", or "api PR (not raised)".
func prRef(s shipRead) string {
	w := s.words()
	if s.PR == nil {
		return s.Project + " " + w.Short + " (not raised)"
	}
	ref := fmt.Sprintf("%s %s%d", s.Project, w.Sigil, s.PR.Number)
	if ciOf(s.Checks) == registry.CheckFailed {
		ref += " · CI failing"
	}
	return ref
}
