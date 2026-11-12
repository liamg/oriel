package ship_test

import (
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// A raised PR's overview says where it stands at a glance: Open, then Approved once a reviewer approves on GitHub,
// then Merged, which outranks the approval.
func TestARaisedPRsBadgeSaysWhereItStands(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := shipOf(s)
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the PR open", func() bool { return s.Find("Pull request: Open") != nil })
	if s.Find("Pull request open") != nil {
		t.Error("an open PR's view is framed")
	}

	hub.AddReview("acme/search-api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewApproved})
	s.Run("github.sync", registry.Args{"id": string(se.ID)})
	s.Until("approved, the view framed so", func() bool {
		return s.Find("Pull request: Approved") != nil && s.Find("Pull request approved") != nil
	})

	hub.Merge("acme/search-api", pr.Number)
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the PR merged", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil && sh.PR.Merged })
	s.Until("merged, over the approval, the view framed so", func() bool {
		return s.Find("Pull request: Merged") != nil && s.Find("Pull request merged") != nil
	})
	if s.Find("Pull request: Approved") != nil {
		t.Error("the approval still shows over the merge")
	}
}
