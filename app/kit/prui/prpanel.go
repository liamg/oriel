// Package prui is the pull request views the Ship and Reviews features share: a PR's box, its checks and reviewers,
// and its status line.
package prui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// A pull request's status boxes, shared by the Ship panel and the review pages: Checks and Reviews, each a box with
// lines and a footer saying how fresh it is.

var (
	TPRCard    = theme.Declare("ship.card.border", "line.strong")
	TPRCardBg  = theme.Declare("ship.card.bg", "bg.base")
	TPRTodo    = theme.Declare("ship.step.todo", "amber")
	TPRLink    = theme.Declare("ship.link", "violet")
	TPRRunning = theme.Declare("ship.ci.running", "status.ci")
)

// PRBox is a titled box: its rows, and a footer saying how fresh they are (nil for none).
func PRBox(c *ui.Ctx, key, title string, body func(c *ui.Ctx), foot func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Radius: 6, Stroke: th.C(TPRCard), Border: 1, Fill: th.C(TPRCardBg), Clip: true}, func(c *ui.Ctx) {
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8), Gap: 2}, func(c *ui.Ctx) {
			kit.SectionCap(c, title)
			body(c)
		})
		if foot != nil {
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 5), Clip: true}, foot)
		}
	})
}

// PRLine is a row of a box: a glyph in the state's colour (ok, warn, fail, doing, todo), a label, and a word on the
// right.
func PRLine(c *ui.Ctx, key, state, label, note string) {
	th := c.Theme()
	glyph, tone := PRGlyph(c, state)
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: label + " " + note}, func(c *ui.Ctx) {
		c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(12.5, tone)) })
		c.Row("l", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("l", label, kit.UIFont(12.5, th.C(theme.Fg))) })
		c.Text("n", note, kit.MonoFont(10.5, prNoteTone(c, state)))
	})
}

// PRGlyph is a line's state as a glyph and its colour.
func PRGlyph(c *ui.Ctx, state string) (string, color.NRGBA) {
	th := c.Theme()
	switch state {
	case "warn":
		return "!", th.C(theme.Warn)
	case "fail":
		return "✗", th.C(theme.Error)
	case "doing":
		return "◌", th.C(TPRRunning)
	case "todo":
		return "○", th.C(theme.FgFaint)
	}
	return "✓", th.C(theme.OK)
}

// prNoteTone colours a line's word: the state's colour when it needs doing, muted when it's fine.
func prNoteTone(c *ui.Ctx, state string) color.NRGBA {
	if state == "ok" || state == "todo" {
		return c.Theme().C(theme.FgMuted)
	}
	_, tone := PRGlyph(c, state)
	return tone
}

// Freshness is a box's footer: what was read and how long ago, in Warn once it's older than stale.
func Freshness(c *ui.Ctx, what string, at time.Time, stale time.Duration, extra string) {
	th := c.Theme()
	text, tone := what+" "+AgoFine(c.Now(), at), th.C(theme.FgFaint)
	if at.IsZero() {
		text = "not " + what + " yet"
	} else if stale > 0 && c.Now().Sub(at) > stale {
		tone = th.C(theme.Warn)
	}
	if extra != "" {
		text += " · " + extra
	}
	c.Text("f", text, kit.MonoFont(10, tone))
}

// AgoFine is AgoText to the second under a minute: "28s ago".
func AgoFine(now, t time.Time) string {
	if d := now.Sub(t); d >= time.Second && d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	return widget.AgoLong(now, t)
}

// ChecksSummary is a PR's checks in a line: all passed, how many fail, how many run, or none.
func ChecksSummary(checks []registry.Check) (state, text string) {
	failed, pending, passed := 0, 0, 0
	for _, ch := range checks {
		switch {
		case ch.State == registry.CheckFailed:
			failed++
		case ch.Pending():
			pending++
		default:
			passed++
		}
	}
	switch {
	case len(checks) == 0:
		return "todo", "No checks reported"
	case failed > 0:
		return "fail", fmt.Sprintf("%d of %d failing", failed, len(checks))
	case pending > 0:
		return "doing", fmt.Sprintf("%d running, %d passed", pending, passed)
	}
	return "ok", fmt.Sprintf("All checks passed (%d)", len(checks))
}

// ChecksBody is the Checks box's rows: the summary, then each failing or running check. It returns the summary
// row, for text wrapped under it.
func ChecksBody(c *ui.Ctx, checks []registry.Check) ui.ID {
	state, text := ChecksSummary(checks)
	glyph, tone := PRGlyph(c, state)
	sum := c.Row("sum", ui.Style{W: ui.Flex(1), H: ui.Px(24), Gap: 8, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(13, tone)) })
		c.Text("t", text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: tone})
	})
	now := c.Now()
	for i, ch := range checks {
		if ch.State == registry.CheckFailed || ch.Pending() {
			CheckRow(c, i, ch, now)
		}
	}
	return sum
}

// CheckRow is one check: its state, name and how long it took or has run.
func CheckRow(c *ui.Ctx, key any, ch registry.Check, now time.Time) {
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
			glyph, tone = "◌", th.C(TPRRunning)
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

// WrapUnder is text wrapped to the width row had last frame, indented under its glyph; cut to a line until it's laid
// out.
func WrapUnder(c *ui.Ctx, key any, row ui.ID, text string, ts ui.TextStyle) {
	c.Row(key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 22}, Clip: true}, func(c *ui.Ctx) {
		if r, ok := c.Rect(row); ok && r.Dx() > 60 {
			widget.Wrap(c, "w", text, r.Dx()-22, ts)
			return
		}
		c.Text("t", text, ts)
	})
}

// ReviewersBox is the Reviews box: each reviewer with where they stand, approved, changes, commented or waiting.
func ReviewersBox(c *ui.Ctx, reviewers []registry.PRReviewer) {
	th := c.Theme()
	reviewers = MergeReviewers(reviewers)
	PRBox(c, "reviews", "REVIEWS", func(c *ui.Ctx) {
		if len(reviewers) == 0 {
			c.Row("none", ui.Style{W: ui.Flex(1), H: ui.Px(22), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", "No reviewers yet", kit.UIFont(12, th.C(theme.FgMuted)))
			})
			return
		}
		for _, r := range reviewers {
			word, tone := "waiting", th.C(theme.FgMuted)
			switch r.State {
			case registry.ReviewApproved:
				word, tone = "approved", th.C(theme.OK)
			case registry.ReviewChanges:
				word, tone = "changes", th.C(theme.Error)
			case registry.ReviewCommented:
				word = "commented"
			case registry.ReviewReRequested:
				word, tone = "re-requested", th.C(theme.Accent)
			}
			c.Row(r.Login, ui.Style{W: ui.Flex(1), H: ui.Px(22), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: r.Login + " " + word}, func(c *ui.Ctx) {
				ReviewerAvatar(c, r.Login)
				c.Row("n", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("n", ReviewerName(r.Login), kit.UIFont(12, th.C(theme.Fg))) })
				c.Text("s", word, kit.MonoFont(10.5, tone))
			})
		}
	}, nil)
}

// MergeReviewers lists each reviewer once, where they first come: one who's asked and has also reviewed (asked again
// since) is re-requested, not waiting and reviewed both.
func MergeReviewers(rs []registry.PRReviewer) []registry.PRReviewer {
	var out []registry.PRReviewer
	at := map[string]int{}
	for _, r := range rs {
		i, seen := at[r.Login]
		switch {
		case !seen:
			at[r.Login] = len(out)
			out = append(out, r)
		case out[i].State == "requested" || r.State == "requested":
			out[i].State = registry.ReviewReRequested
		default:
			out[i].State = r.State // the later review
		}
	}
	return out
}

// ReviewerName is a reviewer as shown: a team without its org.
func ReviewerName(r string) string {
	if _, t, ok := strings.Cut(r, "/"); ok {
		return t
	}
	return r
}

// ReviewerAvatar is a reviewer's initials on a tint: a team's in one colour, a person's in another.
func ReviewerAvatar(c *ui.Ctx, who string) {
	th := c.Theme()
	tone := th.C(TPRLink)
	if !strings.Contains(who, "/") {
		tone = th.C(TPRTodo)
	}
	c.Box("av", ui.Style{W: ui.Px(17), H: ui.Px(17), Radius: 4, Fill: tone, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("i", kit.ProjectInitials(ReviewerName(who)), kit.Mono(8, typeset.Bold, th.C(theme.AccentInk)))
	})
}

// PRStateTone is the colour of a PR's state chip: open, draft, merged, closed, drafting or not raised.
func PRStateTone(c *ui.Ctx, word string) color.NRGBA {
	th := c.Theme()
	switch word {
	case "merged":
		return th.C(theme.StatusPermission)
	case "closed":
		return th.C(theme.FgFaint)
	case "draft", "not raised":
		return th.C(theme.FgMuted)
	case "drafting":
		return th.C(TPRTodo)
	}
	return th.C(theme.OK)
}
