package ship_test

import (
	"context"
	"testing"

	gh "github.com/liamg/oriel/integ/github"
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
	s.Run("review.threads.sync", registry.Args{"id": string(se.ID)})
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

// An approval that no longer stands, given before your latest push or by a reviewer you've asked again since, isn't
// counted as one: the badge says Needs re-review, not Approved, and the Reviews box says re-review for that reviewer.
func TestAnApprovalBeforeTheLatestPushNeedsReReview(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	hub.AddCommit("acme/search-api", pr.Number, "c1")
	hub.AddReview("acme/search-api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewApproved, Commit: "c1"})
	s.Run("ship.refresh", nil)
	s.Run("review.threads.sync", registry.Args{"id": string(se.ID)})
	s.Run("project.open", registry.Args{"project": "search-api"})
	s.Run("project.panel.toggle", registry.Args{"name": "pull-request"}) // its Reviews box
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})        // its badge
	s.Until("approved at the head", func() bool { return s.Find("Pull request: Approved") != nil && s.Find("priya-k approved") != nil })

	hub.AddCommit("acme/search-api", pr.Number, "c2")
	s.Run("ship.refresh", nil)
	s.Run("review.threads.sync", registry.Args{"id": string(se.ID)})
	s.Until("the approval of c1 needing re-review once c2 is pushed", func() bool {
		return s.Find("Pull request: Needs re-review") != nil && s.Find("priya-k re-review") != nil
	})
	if s.Find("Pull request: Approved") != nil || s.Find("Pull request approved") != nil {
		t.Error("an approval of an older commit still shows as approved")
	}

	// Approved again at the head, then asked again: it no longer stands either.
	hub.AddReview("acme/search-api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewApproved, Commit: "c2"})
	s.Run("review.threads.sync", registry.Args{"id": string(se.ID)})
	s.Until("approved again at the head", func() bool { return s.Find("Pull request: Approved") != nil })
	if err := hub.Client().RequestReviewers(context.Background(), gh.Repo{Owner: "acme", Name: "search-api"}, pr.Number, []string{"priya-k"}); err != nil {
		t.Fatal(err)
	}
	s.Run("ship.refresh", nil)
	s.Run("review.threads.sync", registry.Args{"id": string(se.ID)})
	s.Until("the approval needing re-review once she's asked again", func() bool {
		return s.Find("Pull request: Needs re-review") != nil && s.Find("priya-k re-review") != nil
	})
	if s.Find("priya-k waiting") != nil {
		t.Error("a reviewer asked again after approving shows twice")
	}
}
