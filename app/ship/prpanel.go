package ship

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The dock's Pull request panel: a header with the PR's state, then its Checks, Reviews and Branch, each in a box
// that says how fresh it is.

// prPanelState is where a project's PR stands, in a word: open, draft, merged, closed, drafting or not raised.
func (x *Ship) prPanelState(se core.SessionState, s shipRead) string {
	switch {
	case s.PR != nil:
		return prStateWord(s.PR)
	case !s.PRDraft.Empty() || x.prForm(se.ID, s).edited:
		return "drafting"
	}
	return "not raised"
}

func (x *Ship) panelHeader(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	w := s.words()
	title := cmp.Or(s.PRDraft.Title, "Not raised")
	if s.PR != nil {
		title = s.PR.Title
	}
	c.Row("title", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
	})
	sub := kit.Counted(s.Commits, "commit")
	if s.PR != nil {
		sub = fmt.Sprintf("%s%d · %s", w.Sigil, s.PR.Number, sub)
	}
	c.Row("sub", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		word := x.prPanelState(se, s)
		widget.Chip(c, "state", word, kit.PRStateTone(c, word), "git/ship#pr")
		c.Row("s", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("s", sub, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
		if widget.IconButton(c, "refresh", "↻", widget.IconButtonOpts{Label: "Read again: fetch origin, ask " + cmp.Or(labelOf(s), "the code host"),
			Help: "git/ship#pr", Disabled: x.report(se.ID).loading}) {
			x.refresh(se.ID, true)
		}
	})
	c.Row("branch", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Text("b", s.Branch+" → "+cmp.Or(s.Base, "?"), kit.MonoFont(10.5, th.C(theme.FgFaint)))
	})
}

// checksFoot is the Checks footer: when the code host was asked, at which commit; or why it couldn't be.
func checksFoot(c *ui.Ctx, s shipRead) {
	if s.HostError != "" {
		th := c.Theme()
		text := "couldn't reach " + cmp.Or(labelOf(s), "the code host")
		if !s.PRRead.IsZero() {
			text += " · " + kit.AgoFine(c.Now(), s.PRRead)
		}
		c.Text("f", text, kit.MonoFont(10, th.C(theme.Error)))
		return
	}
	at := ""
	if s.PR != nil && s.PR.Head != "" {
		at = "at " + shortSHA(s.PR.Head)
	}
	kit.Freshness(c, "checked", s.PRRead, 3*prTTL, at)
}

// labelOf is the project's code host's name, "" for none.
func labelOf(s shipRead) string {
	if s.Code == nil {
		return ""
	}
	return s.Code.Label()
}

func shortSHA(h string) string { return h[:min(7, len(h))] }

func (x *Ship) checksBox(c *ui.Ctx, s shipRead) {
	th := c.Theme()
	kit.PRBox(c, "checks", "CHECKS", func(c *ui.Ctx) {
		sum := kit.ChecksBody(c, s.Checks)
		if s.FailLine != "" {
			kit.WrapUnder(c, "fail", sum, s.FailLine, kit.MonoFont(10, th.C(theme.Error)))
		}
		if s.HostError != "" {
			kit.WrapUnder(c, "herr", sum, s.HostError, kit.MonoFont(10, th.C(theme.Error)))
		}
		if t, ok := ciTrouble(s); ok && s.PR.Open() {
			c.Column("ci", ui.Style{Gap: 6, Pad: ui.Insets{T: 4}}, func(c *ui.Ctx) {
				kit.AskTwinsLabelled(c, x.h, "ship.ci."+s.Project, t, "git/ship#checks", "Explain with agent", "Fix CI with agent")
			})
		}
		kit.ExplainKept(c, x.h, "ship.ci."+s.Project, "git/ship#checks")
	}, func(c *ui.Ctx) { checksFoot(c, s) })
}

// reviewersOf is who's asked to review a PR or has reviewed it, each with where they stand: approved, changes or
// waiting.
func (x *Ship) reviewersOf(s shipRead) (who []string, state map[string]string) {
	x.mu.Lock()
	state = maps.Clone(x.reviewed[s.PR.URL])
	x.mu.Unlock()
	who = slices.Clone(s.PR.Reviewers)
	for r := range state {
		if !slices.Contains(who, r) {
			who = append(who, r)
		}
	}
	slices.Sort(who[len(s.PR.Reviewers):])
	return who, state
}

func (x *Ship) reviewsBox(c *ui.Ctx, s shipRead) {
	who, state := x.reviewersOf(s)
	rs := make([]registry.PRReviewer, len(who))
	for i, r := range who {
		rs[i] = registry.PRReviewer{Login: r, State: state[r]}
	}
	kit.ReviewersBox(c, rs)
}

// remoteAhead says the PR's head isn't the commit checked out here, with nothing here to push: someone pushed.
func remoteAhead(s shipRead) bool {
	if s.PR == nil || s.PR.Head == "" || s.Head == "" || len(s.Unpushed) > 0 {
		return false
	}
	n := min(len(s.Head), len(s.PR.Head))
	return s.Head[:n] != s.PR.Head[:n]
}

// branchLine is a line of the Branch box: its state (ok, warn, todo), label and word.
type branchLine struct{ key, state, label, note string }

// branchLines are the Branch box's lines: committed, up to date, pushed, and whether origin has moved on.
func branchLines(s shipRead) []branchLine {
	var out []branchLine
	if len(s.Files) > 0 {
		out = append(out, branchLine{"committed", "warn", "Committed", kit.Counted(len(s.Files), "file") + " left"})
	} else {
		out = append(out, branchLine{"committed", "ok", "Committed", "clean"})
	}
	switch {
	case s.Behind > 0:
		out = append(out, branchLine{"uptodate", "warn", "Up to date", fmt.Sprintf("%d behind %s", s.Behind, s.Base)})
	case s.BaseAhead > 0:
		out = append(out, branchLine{"uptodate", "warn", s.Base + " pushed", fmt.Sprintf("%d to push", s.BaseAhead)})
	default:
		out = append(out, branchLine{"uptodate", "ok", "Up to date", "with " + cmp.Or(s.Base, "base")})
	}
	switch {
	case len(s.Unpushed) > 0:
		out = append(out, branchLine{"pushed", "warn", "Pushed", fmt.Sprintf("%d to push", len(s.Unpushed))})
	case s.Pushed:
		out = append(out, branchLine{"pushed", "ok", "Pushed", "yes"})
	default:
		out = append(out, branchLine{"pushed", "todo", "Pushed", "not pushed"})
	}
	switch {
	case remoteAhead(s):
		out = append(out, branchLine{"remote", "warn", "On origin", "new commits"})
	case s.PR != nil && s.PR.Head != "" && s.Head != "":
		out = append(out, branchLine{"remote", "ok", "On origin", "in sync"})
	}
	return out
}

func (x *Ship) branchBox(c *ui.Ctx, s shipRead) {
	kit.PRBox(c, "branch", "BRANCH", func(c *ui.Ctx) {
		for _, l := range branchLines(s) {
			kit.PRLine(c, l.key, l.state, l.label, l.note)
		}
	}, func(c *ui.Ctx) { kit.Freshness(c, "git read", s.GitRead, 0, "") })
}

// checklistBody is the Pull request panel once the project's been read.
func (x *Ship) checklistBody(c *ui.Ctx, se core.SessionState, p string) {
	th := c.Theme()
	s, _ := x.ship(se.ID, p)
	w := s.words()
	x.panelHeader(c, se, s)
	c.Box("sp", ui.Style{H: ui.Px(6)}, nil)
	if s.PR != nil {
		x.checksBox(c, s)
		x.reviewsBox(c, s)
	} else if s.HostError != "" && s.Code != nil {
		c.Row("err", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { checksFoot(c, s) })
	}
	x.branchBox(c, s)
	c.Box("sp2", ui.Style{H: ui.Px(4)}, nil)
	c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		if s.PR != nil && widget.Button(c, "open", "Open "+w.Short, widget.ButtonOpts{Help: "git/ship#pr"}) {
			x.run("ship.pr.open", registry.Args{"project": p})
		}
		if s.PR.Open() {
			x.prActions(c, se, s)
		} else {
			x.nextButton(c, se, s, true)
			if widget.Button(c, "ship", "Ship ⇉", widget.ButtonOpts{Help: "git/ship#page"}) {
				x.run("ship.open", nil)
			}
		}
	})
	note := "opens the Raise " + w.Short + " tab →"
	switch {
	case s.PR.Open():
		note = "Open " + w.Short + " opens as a main tab"
	case movesOn(s) && registry.Can(x.h, se.Kind, core.NextPR):
		note = "Start next " + w.Short + " cuts a fresh branch from " + cmp.Or(s.Base, "the base")
	}
	c.Text("note", note, kit.MonoFont(10, th.C(theme.FgGhost)))
}

// reviewStates is each reviewer's latest verdict among a PR's review threads: approved or changes over commented.
func reviewStates(ts []registry.ReviewThread) map[string]string {
	out := map[string]string{}
	for _, t := range ts {
		if t.Reviewer == "" || t.State == "" {
			continue
		}
		if t.State == registry.ReviewCommented && out[t.Reviewer] != "" {
			continue
		}
		out[t.Reviewer] = t.State
	}
	return out
}
