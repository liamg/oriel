package kit

import (
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// PRStatus is where a pull request stands, as its badge shows it: a word, an icon and a tone.
type PRStatus struct {
	Word string
	Icon string
	Tone theme.TokenID
}

// PRStatusOf is a PR's status from its state ("open", "closed", "merged"), whether it's merged or a draft, and its
// reviewers' latest verdicts (GitHub's "APPROVED" or a code host's registry.ReviewApproved…): merged or closed over
// anything a reviewer said, changes requested over an approval, then approved, draft, open.
func PRStatusOf(state string, merged, draft bool, reviews []string) PRStatus {
	switch {
	case merged || state == "merged":
		return PRStatus{"Merged", "merged", theme.StatusPermission}
	case state == "closed":
		return PRStatus{"Closed", "prclosed", theme.Error}
	}
	approved := false
	for _, r := range reviews {
		switch strings.ToLower(r) {
		case "changes_requested", registry.ReviewChanges:
			return PRStatus{"Changes requested", "changes", theme.Warn}
		case "approved":
			approved = true
		}
	}
	switch {
	case approved:
		return PRStatus{"Approved", "approved", theme.OK}
	case draft:
		return PRStatus{"Draft", "pullrequest", theme.FgMuted}
	}
	return PRStatus{"Open", "pullrequest", theme.OK}
}

// PRBadge draws a PR's status where it's seen at a glance: its icon, word and outline in its tone.
func PRBadge(c *ui.Ctx, r registry.Registry, key any, s PRStatus, help string) {
	th := c.Theme()
	tone := th.C(s.Tone)
	c.Row(key, ui.Style{H: ui.Px(30), Pad: ui.PadXY(10, 0), Gap: 7, Cross: ui.Center, Radius: 7, Stroke: theme.WithAlpha(tone, .75), Border: 1, Role: ui.RoleStatus, Label: "Pull request: " + s.Word, Help: help}, func(c *ui.Ctx) {
		Icon(c, r, "i", s.Icon, 18, tone)
		c.Text("w", s.Word, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13.5, Weight: typeset.SemiBold}, Color: tone})
	})
}

// PRReviewsService gives a PR's reviewers' latest verdicts, for a code host that keeps them (GitHub's mirror).
const PRReviewsService = "pr.reviews"

// PRReviews is the PRReviewsService.
type PRReviews interface {
	// Reviews are the latest verdict of each reviewer of the PR at url in session id, as the host says it.
	Reviews(id core.SessionID, url string) []string
}

// PRReviewsOf are the reviewers' latest verdicts on the PR at url in session id, from whichever plugin keeps them.
func PRReviewsOf(r registry.Registry, id core.SessionID, url string) []string {
	if s, ok := registry.ServiceOf[PRReviews](r, PRReviewsService); ok {
		var out []string
		registry.Call(func() error { out = s.Reviews(id, url); return nil })
		return out
	}
	return nil
}

// Framed says the status is one worth framing the whole PR view in: something happened to it (merged, closed) or a
// reviewer has decided (approved, changes requested). An open PR, or a draft, isn't framed.
func (s PRStatus) Framed() bool {
	switch s.Word {
	case "Merged", "Closed", "Approved", "Changes requested":
		return true
	}
	return false
}

// TReviewCard is the border of a review page's cards, which PRFrame recolours with TPRCard's.
var TReviewCard = theme.Declare("review.card.border", "line")

// PRCardStroke is the border the cards of a PR's view take when its status is Framed.
func PRCardStroke(th *theme.Resolved, s PRStatus) theme.Color {
	return theme.WithAlpha(th.C(s.Tone), .75)
}

// PRFrame lays fn out filling its parent; when the status is Framed, the cards inside (TPRCard, TReviewCard) are
// bordered in its tone, so the whole view says where the PR stands, not only its badge.
func PRFrame(c *ui.Ctx, key any, s PRStatus, fn func(c *ui.Ctx)) {
	st := ui.Style{W: ui.Flex(1), H: ui.Flex(1)}
	if s.Framed() {
		th := c.Theme()
		edge := PRCardStroke(th, s)
		st.Theme = th.With(map[theme.TokenID]theme.Color{TPRCard: edge, TReviewCard: edge})
		st.Role, st.Label = ui.RoleGroup, "Pull request "+strings.ToLower(s.Word)
	}
	c.Column(key, st, fn)
}
