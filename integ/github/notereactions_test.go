package github_test

import (
	"context"
	"reflect"
	"testing"

	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/integ/github/ghtest"
)

// Each comment on a PR (a line comment, a conversation comment, a review's body) is read with its reactions and
// whether the user is among them and who the first are, in one query; the user reacts to one by its node and takes a reaction back.
func TestCommentReactions(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			pr := s.AddPR("acme/api", gh.PR{Title: "feat: round", Head: "pricing-rounding", Base: "main"})
			line := s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "round.go", Line: 80, Body: "An error instead?"})
			rv := s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewCommented, Body: "A few things."})
			ic := s.AddIssueComment("acme/api", pr.Number, gh.IssueComment{User: "bo", Body: "When does this ship?"})
			s.ReactAs("jkowalski", ghtest.LineComment, line.ID, "+1")
			s.ReactAs("you", ghtest.LineComment, line.ID, "+1")
			s.ReactAs("bo", ghtest.ReviewBody, rv.ID, "rocket")

			got, err := c.CommentReactions(ctx, acme, pr.Number)
			if err != nil {
				t.Fatal(err)
			}
			want := []gh.Reaction{{Name: "+1", Count: 2, Mine: true, Who: []string{"jkowalski", "you"}}}
			if n := got.Line[line.ID]; n.Node == "" || !reflect.DeepEqual(n.Reactions, want) {
				t.Errorf("the line comment: %+v", n)
			}
			if n := got.Review[rv.ID]; n.Node == "" || !reflect.DeepEqual(n.Reactions, []gh.Reaction{{Name: "rocket", Count: 1, Who: []string{"bo"}}}) {
				t.Errorf("the review: %+v", n)
			}
			n := got.Issue[ic.ID]
			if n.Node == "" || len(n.Reactions) != 0 {
				t.Fatalf("the conversation comment: %+v", n)
			}

			if err := c.ReactNode(ctx, n.Node, "+1", true); err != nil {
				t.Fatal(err)
			}
			if err := c.ReactNode(ctx, got.Line[line.ID].Node, "+1", false); err != nil {
				t.Fatal(err)
			}
			got, err = c.CommentReactions(ctx, acme, pr.Number)
			if err != nil {
				t.Fatal(err)
			}
			if r := got.Issue[ic.ID].Reactions; !reflect.DeepEqual(r, []gh.Reaction{{Name: "+1", Count: 1, Mine: true, Who: []string{"you"}}}) {
				t.Errorf("after reacting 👍: %+v", r)
			}
			if r := got.Line[line.ID].Reactions; !reflect.DeepEqual(r, []gh.Reaction{{Name: "+1", Count: 1, Who: []string{"jkowalski"}}}) {
				t.Errorf("after taking the user's 👍 back: %+v", r)
			}
			if err := c.ReactNode(ctx, n.Node, "party", true); err == nil {
				t.Error("a reaction GitHub doesn't have was taken")
			}
		})
	}
}
