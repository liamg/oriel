package github_test

import (
	"context"
	"reflect"
	"testing"

	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/integ/github/ghtest"
)

// Each comment on a PR (a line comment, a conversation comment, a review's body) is read by the node REST gives it,
// with its reactions and whether the user is among them and who the first are, in one query; the user reacts to one
// by its node and takes a reaction back. REST's own counts show which comments have reactions.
func TestCommentReactions(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			pr := s.AddPR("acme/api", gh.PR{Title: "feat: round", Head: "pricing-rounding", Base: "main"})
			line := s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "round.go", Line: 80, Body: "An error instead?"})
			rv := s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewCommented, Body: "A few things."})
			s.AddIssueComment("acme/api", pr.Number, gh.IssueComment{User: "bo", Body: "When does this ship?"})
			s.ReactAs("jkowalski", ghtest.LineComment, line.ID, "+1")
			s.ReactAs("you", ghtest.LineComment, line.ID, "+1")
			s.ReactAs("bo", ghtest.ReviewBody, rv.ID, "rocket")

			lines, _ := c.ReviewComments(ctx, acme, pr.Number)
			reviews, _ := c.Reviews(ctx, acme, pr.Number)
			issue, _ := c.IssueComments(ctx, acme, pr.Number)
			if len(lines) != 1 || len(reviews) != 1 || len(issue) != 1 || lines[0].Node == "" || reviews[0].Node == "" || issue[0].Node == "" {
				t.Fatalf("REST gave %+v, %+v, %+v", lines, reviews, issue)
			}
			if !reflect.DeepEqual(lines[0].Reactions, gh.Reactions{"+1": 2}) || len(issue[0].Reactions) != 0 {
				t.Errorf("REST counts %v on the line comment, %v on the conversation's", lines[0].Reactions, issue[0].Reactions)
			}
			nodes := []string{lines[0].Node, reviews[0].Node, issue[0].Node}
			got, err := c.CommentReactions(ctx, nodes)
			if err != nil {
				t.Fatal(err)
			}
			want := []gh.Reaction{{Name: "+1", Count: 2, Mine: true, Who: []string{"jkowalski", "you"}}}
			if n := got[lines[0].Node]; n.Node != lines[0].Node || !reflect.DeepEqual(n.Reactions, want) {
				t.Errorf("the line comment: %+v", n)
			}
			if n := got[reviews[0].Node]; !reflect.DeepEqual(n.Reactions, []gh.Reaction{{Name: "rocket", Count: 1, Who: []string{"bo"}}}) {
				t.Errorf("the review: %+v", n)
			}
			if n := got[issue[0].Node]; len(n.Reactions) != 0 {
				t.Fatalf("the conversation comment: %+v", n)
			}

			if err := c.ReactNode(ctx, issue[0].Node, "+1", true); err != nil {
				t.Fatal(err)
			}
			if err := c.ReactNode(ctx, lines[0].Node, "+1", false); err != nil {
				t.Fatal(err)
			}
			got, err = c.CommentReactions(ctx, nodes)
			if err != nil {
				t.Fatal(err)
			}
			if r := got[issue[0].Node].Reactions; !reflect.DeepEqual(r, []gh.Reaction{{Name: "+1", Count: 1, Mine: true, Who: []string{"you"}}}) {
				t.Errorf("after reacting 👍: %+v", r)
			}
			if r := got[lines[0].Node].Reactions; !reflect.DeepEqual(r, []gh.Reaction{{Name: "+1", Count: 1, Who: []string{"jkowalski"}}}) {
				t.Errorf("after taking the user's 👍 back: %+v", r)
			}
			if err := c.ReactNode(ctx, issue[0].Node, "party", true); err == nil {
				t.Error("a reaction GitHub doesn't have was taken")
			}
		})
	}
}

// More comments than one query takes are read 100 at a time; the last still has its reactions.
func TestCommentReactionsPastTheFirstHundred(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			pr := s.AddPR("acme/api", gh.PR{Title: "feat: round", Head: "pricing-rounding", Base: "main"})
			var rv gh.Review
			for range 150 {
				rv = s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewCommented, Body: "Another reply."})
			}
			s.ReactAs("bo", ghtest.ReviewBody, rv.ID, "rocket")
			reviews, _ := c.Reviews(context.Background(), acme, pr.Number)
			var nodes []string
			for _, r := range reviews {
				nodes = append(nodes, r.Node)
			}
			got, err := c.CommentReactions(context.Background(), nodes)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(nodes) || s.ReactionReads() != 2 {
				t.Fatalf("read %d of %d comments in %d queries, want all in 2", len(got), len(nodes), s.ReactionReads())
			}
			if r := got[nodes[len(nodes)-1]].Reactions; !reflect.DeepEqual(r, []gh.Reaction{{Name: "rocket", Count: 1, Who: []string{"bo"}}}) {
				t.Errorf("the last review: %+v", r)
			}
		})
	}
}
