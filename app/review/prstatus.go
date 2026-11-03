package review

import (
	"cmp"
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// prStatus is what a pull request's status boxes show: its detail once read, and the fields only its page knows.
type prStatus struct {
	full   registry.ReviewDetail
	done   bool
	read   time.Time
	host   string      // the code host's name
	fields [][2]string // label, value: under the Pull request box's lines
}

// prStatusBoxes are a pull request's Checks, Pull request and Reviews boxes, as the Ship panel draws them.
func prStatusBoxes(c *ui.Ctx, s prStatus) {
	full := s.full
	kit.PRBox(c, "checks", "CHECKS", func(c *ui.Ctx) {
		if !s.done {
			kit.PRLine(c, "asking", "doing", "Checks", "asking "+cmp.Or(s.host, "the code host"))
			return
		}
		kit.ChecksBody(c, full.Checks)
	}, func(c *ui.Ctx) {
		at := ""
		if full.Commit != "" {
			at = "at " + kit.ShortSHA(full.Commit)
		}
		kit.Freshness(c, "checked", s.read, 0, at)
	})
	kit.PRBox(c, "pr", "PULL REQUEST", func(c *ui.Ctx) {
		if !s.done {
			kit.PRLine(c, "state", "doing", "State", "asking")
		} else {
			kit.PRLine(c, "state", prStateLine(full.State), "State", cmp.Or(full.State, "unknown"))
			state, note := mergeLine(full.Mergeable)
			kit.PRLine(c, "mergeable", state, "Mergeable", note)
		}
		for _, f := range s.fields {
			kit.PreviewField(c, f[0], f[1])
		}
		if s.done && len(full.Labels) > 0 {
			kit.PreviewField(c, "labels", strings.Join(full.Labels, " · "))
		}
	}, nil)
	if s.done {
		kit.ReviewersBox(c, full.Reviewers)
	}
}

// prStateLine is a PR's state as a line's state: open is fine, closed is done with, unknown is to find out.
func prStateLine(state string) string {
	switch state {
	case "open", "merged":
		return "ok"
	}
	return "todo"
}

// mergeLine is a PR's mergeability as a line: "yes" is fine, conflicts fail, anything else wants a look.
func mergeLine(m string) (state, note string) {
	switch {
	case m == "":
		return "todo", "unknown"
	case m == "yes":
		return "ok", m
	case strings.Contains(m, "conflict"):
		return "fail", m
	}
	return "warn", m
}

// prDescription is the PR's description, and the reactions to it.
func prDescription(c *ui.Ctx, r registry.Registry, full registry.ReviewDetail, width float32) {
	if strings.TrimSpace(full.Body) == "" && len(full.Reactions) == 0 {
		return
	}
	th := c.Theme()
	reviewCard(c, "description", "DESCRIPTION", ui.Flex(1), func(c *ui.Ctx) {
		if strings.TrimSpace(full.Body) != "" {
			kit.MarkdownText(c, r, "body", full.Body, width, kit.UIFont(13, th.C(theme.FgMuted)))
		}
		reactions(c, "reactions", full.Reactions)
	})
}

// reactionWords are the common reactions as the renderer can draw them: it has no colour emoji.
var reactionWords = map[string]string{"👍": "+1", "👎": "−1", "😄": "laugh", "🎉": "hooray", "😕": "confused", "❤️": "♥", "❤": "♥",
	"🚀": "rocket", "👀": "eyes"}

// reactions are emoji reactions, each with how many reacted so.
func reactions(c *ui.Ctx, key string, rs []registry.PRReaction) {
	if len(rs) == 0 {
		return
	}
	th := c.Theme()
	c.Row(key, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
		for i, re := range rs {
			c.Row(i, ui.Style{Pad: ui.PadXY(7, 2), Gap: 4, Cross: ui.Center, Radius: 10, Stroke: th.C(theme.Line), Border: 1,
				Role: ui.RoleText, Label: fmt.Sprintf("%s %d", re.Emoji, re.Count)}, func(c *ui.Ctx) {
				c.Text("e", cmp.Or(reactionWords[re.Emoji], re.Emoji), kit.UIFont(12, th.C(theme.Fg)))
				c.Text("n", fmt.Sprint(re.Count), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			})
		}
	})
}

// activityCap is as much of a review's or comment's body as the activity shows.
const activityCap = 600

// prActivity is a pull request's history under its description: each push, review and comment, oldest first.
func prActivity(c *ui.Ctx, r registry.Registry, events []registry.PREvent, width float32) {
	if len(events) == 0 {
		return
	}
	th := c.Theme()
	reviewCard(c, "activity", fmt.Sprintf("ACTIVITY  %d", len(events)), ui.Flex(1), func(c *ui.Ctx) {
		for i, e := range events {
			glyph, verb, tone := activityLine(c, e)
			c.Column(i, ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
				c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(12, tone)) })
					c.Text("who", cmp.Or(e.Who, "someone"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold},
						Color: th.C(theme.Fg)})
					c.Row("verb", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("v", verb, kit.UIFont(12.5, th.C(theme.FgMuted))) })
					if !e.At.IsZero() {
						c.Text("at", kit.AgoText(c.Now(), e.At), kit.MonoFont(10.5, th.C(theme.FgFaint)))
					}
				})
				if body := activityBody(e); body != "" || len(e.Reactions) > 0 {
					c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 22}, Gap: 6}, func(c *ui.Ctx) {
						if body != "" {
							kit.MarkdownText(c, r, "md", body, width-46, kit.UIFont(12.5, th.C(theme.FgMuted)))
						}
						reactions(c, "reactions", e.Reactions)
					})
				}
			})
		}
	})
}

// activityLine is an event's glyph, what it says after who did it, and its colour.
func activityLine(c *ui.Ctx, e registry.PREvent) (glyph, verb string, tone color.NRGBA) {
	th := c.Theme()
	switch e.Kind {
	case registry.PREventPush:
		return "↑", "pushed " + kit.ShortSHA(e.Ref) + "  " + e.Text, th.C(kit.TPRRunning)
	case registry.PREventReview:
		switch e.State {
		case "approved":
			return "✓", "approved", th.C(theme.OK)
		case "changes":
			return "✗", "requested changes", th.C(theme.Error)
		case "dismissed":
			return "–", "had a review dismissed", th.C(theme.FgFaint)
		}
		return "●", "reviewed", th.C(theme.FgMuted)
	}
	if e.Ref != "" {
		return "●", "commented on " + e.Ref, th.C(theme.FgMuted)
	}
	return "●", "commented", th.C(theme.FgMuted)
}

// activityBody is what an event says below its line: a review's or comment's body, cut to activityCap.
func activityBody(e registry.PREvent) string {
	if e.Kind == registry.PREventPush {
		return ""
	}
	body := strings.TrimSpace(e.Text)
	if r := []rune(body); len(r) > activityCap {
		body = strings.TrimSpace(string(r[:activityCap])) + "…"
	}
	return body
}
