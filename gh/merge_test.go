package gh_test

import (
	"context"
	"slices"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// hosts.md, "The merge guard": GitHub's blockers come from one read: unresolved review threads, changes still
// requested, draft, conflicts and mergeStateStatus, with the methods the repo allows and the viewer's default. "With
// sha set to the head we showed, so a push after you looked fails instead of merging unseen code."
func TestMergeStateAndMerge(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			repo := gh.Repo{Owner: "acme", Name: "api"}
			s.SetMergeRepo("acme/api", ghtest.MergeRepo{Methods: []string{"squash", "rebase"}, Default: "rebase"})
			pr := s.AddPR("acme/api", gh.PR{Title: "back off on 429", Head: "eng-2231", Base: "main"})
			s.AddCommit("acme/api", pr.Number, "c1")
			open := s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "a.go", Line: 3, Body: "why?"})
			done := s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "a.go", Line: 9, Body: "nit"})
			s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "you", InReplyTo: open.ID, Body: "because"})
			s.Resolve(done.ID, true)
			s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewChangesRequested})
			s.SetMergeable("acme/api", pr.Number, "MERGEABLE", "BLOCKED")

			m, err := c.MergeState(ctx, repo, pr.Number)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(m.Methods, []string{"squash", "rebase"}) || m.Default != "rebase" || m.SHA != "c1" || m.Unresolved != 1 ||
				!slices.Equal(m.Changes, []string{"priya-k"}) || m.Decision != "CHANGES_REQUESTED" || m.Status != "BLOCKED" || m.Mergeable != "MERGEABLE" {
				t.Fatalf("merge state %+v", m)
			}

			s.AddCommit("acme/api", pr.Number, "c2")
			if err := c.MergePR(ctx, repo, pr.Number, gh.Merge{Method: "squash", SHA: "c1"}); err == nil {
				t.Fatal("merged at a head that had moved on")
			}
			if err := c.MergePR(ctx, repo, pr.Number, gh.Merge{Method: "merge", SHA: "c2"}); err == nil {
				t.Fatal("merged with a method the repo doesn't allow")
			}
			if err := c.MergePR(ctx, repo, pr.Number, gh.Merge{Method: "squash", SHA: "c2", Title: "back off on 429 (#790)"}); err != nil {
				t.Fatal(err)
			}
			if got := s.MergedWith("acme/api", pr.Number); got != "squash" {
				t.Errorf("merged with %q", got)
			}
			if err := c.DeleteBranch(ctx, repo, "liam/eng-2231"); err != nil {
				t.Fatal(err)
			}
			if got := s.Deleted(); !slices.Equal(got, []string{"acme/api:liam/eng-2231"}) {
				t.Errorf("deleted %q", got)
			}
		})
	}
}
