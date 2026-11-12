package review_test

import (
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// A reviewed PR's page says where it stands at a glance: Open, Approved once a reviewer approves, then Merged, which
// outranks the approval.
func TestAReviewedPRsBadgeSaysWhereItStands(t *testing.T) {
	s, _, hub, pr, id, _ := prReviewScene(t, false)
	s.Run("ship.pr.open", nil)
	s.Until("the PR open", func() bool { return s.Find("Pull request: Open") != nil })
	if s.Find("Pull request open") != nil {
		t.Error("an open PR's view is framed")
	}

	hub.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewApproved})
	s.Run("github.sync", registry.Args{"id": string(id)})
	s.Run("ship.pr.open", nil)
	s.Until("approved, the view framed so", func() bool {
		return s.Find("Pull request: Approved") != nil && s.Find("Pull request approved") != nil
	})

	hub.Merge("acme/api", pr.Number)
	s.Run("github.sync", registry.Args{"id": string(id)})
	s.Run("ship.pr.open", nil)
	s.Until("merged, over the approval, the view framed so", func() bool {
		return s.Find("Pull request: Merged") != nil && s.Find("Pull request merged") != nil
	})
}
