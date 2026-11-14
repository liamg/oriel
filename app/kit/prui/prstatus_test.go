package prui

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A PR's status is the one thing that matters most about it: merged or closed over anything a reviewer said, then
// changes requested over an approval, then an approval, then draft, then open. Review states come in either form
// (GitHub's "APPROVED", a code host's "approved").
func TestPRStatusOf(t *testing.T) {
	for _, tt := range []struct {
		state         string
		merged, draft bool
		reviews       []string
		want          string
	}{
		{"open", false, false, nil, "Open"},
		{"open", false, true, nil, "Draft"},
		{"open", false, false, []string{"APPROVED"}, "Approved"},
		{"open", false, false, []string{"approved", "commented"}, "Approved"},
		{"open", false, false, []string{"APPROVED", "CHANGES_REQUESTED"}, "Changes requested"},
		{"open", false, true, []string{"approved"}, "Approved"},
		{"closed", true, false, []string{"CHANGES_REQUESTED"}, "Merged"},
		{"merged", false, false, nil, "Merged"},
		{"closed", false, false, []string{"approved"}, "Closed"},
		{"", false, false, nil, "Open"},
	} {
		if got := PRStatusOf(tt.state, tt.merged, tt.draft, tt.reviews); got.Word != tt.want {
			t.Errorf("%s merged=%v draft=%v %v: %q, want %q", tt.state, tt.merged, tt.draft, tt.reviews, got.Word, tt.want)
		}
	}
}

// Only what happened to a PR, or a reviewer's decision, frames its view: not an open PR, nor a draft.
func TestPRStatusFramed(t *testing.T) {
	for word, want := range map[string]bool{"Merged": true, "Closed": true, "Approved": true, "Changes requested": true, "Open": false, "Draft": false, "": false} {
		if got := (PRStatus{Word: word}).Framed(); got != want {
			t.Errorf("%q framed %v, want %v", word, got, want)
		}
	}
}

// A framed status recolours the borders of the cards inside the view to its tone; an unframed one leaves them as the
// theme has them.
func TestPRFrameRecoloursTheCards(t *testing.T) {
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []PRStatus{PRStatusOf("merged", true, false, nil), PRStatusOf("open", false, false, []string{"approved"}), PRStatusOf("open", false, false, nil)} {
		u := ui.New(headless.Shaper{}, ui.TextStyle{})
		u.SetTheme(th)
		var card, review render.Color
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *ui.Ctx) {
			PRFrame(c, "frame", s, func(c *ui.Ctx) {
				card, review = c.Theme().C(TPRCard), c.Theme().C(TReviewCard)
			})
		})
		want, wantReview := th.C(TPRCard), th.C(TReviewCard)
		if s.Framed() {
			want = PRCardStroke(th, s)
			wantReview = want
		}
		if card != want || review != wantReview {
			t.Errorf("%s: cards %v and %v, want %v and %v", s.Word, card, review, want, wantReview)
		}
	}
}
