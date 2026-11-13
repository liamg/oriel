package gh_test

import (
	"context"
	"slices"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// api.md, "GitHub source": one GraphQL search per list gives each PR's size, who was asked (you or a team), your latest
// review and its commit, the commits since, and the head's checks. "Rule: requested now and I have a review →
// re-requested."
func TestReviewQueue(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			s.AddTeam("acme/platform", "you", "ana")

			direct := s.AddPR("acme/api", gh.PR{Title: "rate-limit search", Author: "mkhan", Reviewers: []string{"you"}})
			s.SetPRSize("acme/api", direct.Number, ghtest.PRSize{Author: "mkhan", Additions: 214, Deletions: 38, Files: []gh.PRFile{{Path: "a.go"}, {Path: "b.go"}}})
			s.AddCommit("acme/api", direct.Number, "d1")
			s.SetChecks("acme/api", "d1", gh.Check{Name: "test", Status: "completed", Conclusion: "failure"})

			again := s.AddPR("acme/billing", gh.PR{Title: "invoices to the worker", Author: "jsato", Reviewers: []string{"you"}})
			s.AddCommit("acme/billing", again.Number, "b1")
			s.AddReview("acme/billing", again.Number, gh.Review{User: "you", State: gh.ReviewChangesRequested})
			s.AddCommit("acme/billing", again.Number, "b2")
			s.AddCommit("acme/billing", again.Number, "b3")
			if err := c.RequestReviewers(ctx, gh.Repo{Owner: "acme", Name: "billing"}, again.Number, []string{"you"}); err != nil {
				t.Fatal(err)
			}

			team := s.AddPR("acme/pricing", gh.PR{Title: "chore(deps): sweep", Author: "bot", Reviewers: []string{"acme/platform"}})

			s.AddPR("acme/web", gh.PR{Title: "not mine to review", Author: "ana", Reviewers: []string{"ana"}})
			commented := s.AddPR("acme/api", gh.PR{Title: "retry S3 uploads", Author: "bnoor"})
			s.AddReview("acme/api", commented.Number, gh.Review{User: "you", State: gh.ReviewCommented})

			got, err := c.ReviewQueue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			by := map[int]gh.Asked{}
			for _, a := range got {
				by[a.Number] = a
			}
			if len(got) != 4 {
				t.Fatalf("queue %+v", got)
			}
			if a := by[direct.Number]; !a.Requested || a.Via != "" || a.Mine != "" || a.Author != "mkhan" || a.Repo != "acme/api" ||
				a.Additions != 214 || a.Deletions != 38 || a.Files != 2 || a.CI != gh.CheckFail || a.Asked.IsZero() || a.URL != direct.URL {
				t.Errorf("asked directly: %+v", a)
			}
			if a := by[again.Number]; !a.Requested || a.Mine != gh.ReviewChangesRequested || a.Since != 2 || !a.Asked.After(again.Created) {
				t.Errorf("re-requested: %+v", a)
			}
			if a := by[team.Number]; !a.Requested || a.Via != "acme/platform" {
				t.Errorf("a team ask: %+v", a)
			}
			if a := by[commented.Number]; a.Requested || a.Mine != gh.ReviewCommented {
				t.Errorf("commented: %+v", a)
			}
		})
	}
}

// Each pull request on the queue says who else is asked, who has reviewed it and how, and which branch goes into
// which, from the same search.
//
// ref:   github-graphql/schema.docs.graphql
// quote: "A list of review requests associated with the pull request."
func TestReviewQueueSaysWhoIsAskedAndWhatGoesWhere(t *testing.T) {
	s := ghtest.New(t)
	c := client(t, "http", s)
	pr := s.AddPR("acme/api", gh.PR{Title: "rate-limit search", Author: "mkhan", Head: "rate-limit", Base: "main",
		Reviewers: []string{"you", "ana", "acme/platform"}})
	s.AddReview("acme/api", pr.Number, gh.Review{User: "jsato", State: gh.ReviewApproved})
	s.AddReview("acme/api", pr.Number, gh.Review{User: "bnoor", State: gh.ReviewCommented})
	s.AddReview("acme/api", pr.Number, gh.Review{User: "bnoor", State: gh.ReviewChangesRequested})
	got, err := c.ReviewQueue(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("queue %+v, %v", got, err)
	}
	a := got[0]
	if !slices.Equal(a.Reviewers, []string{"you", "ana", "acme/platform"}) {
		t.Errorf("asked %v", a.Reviewers)
	}
	if a.Base != "main" || a.Head != "rate-limit" {
		t.Errorf("branches %q ← %q", a.Base, a.Head)
	}
	want := []gh.Review{{User: "jsato", State: gh.ReviewApproved}, {User: "bnoor", State: gh.ReviewChangesRequested}}
	if len(a.Reviews) != len(want) {
		t.Fatalf("reviews %+v", a.Reviews)
	}
	for i, w := range want {
		if a.Reviews[i].User != w.User || a.Reviews[i].State != w.State {
			t.Errorf("review %d = %+v, want %+v", i, a.Reviews[i], w)
		}
	}
}

// Your move is answered by a review of any kind, a comment-only one too, or a comment in the conversation, made since
// you were last asked: until you're asked again, the pull request waits on its author, not you. Asked again after a
// review that only commented, it's re-requested.
//
// ref:   github-graphql/schema.docs.graphql
// quote: "A list of reviews associated with the pull request."
func TestReviewQueueCountsCommentsAsAnswers(t *testing.T) {
	s := ghtest.New(t)
	c := client(t, "http", s)
	ctx := context.Background()
	again := s.AddPR("acme/coast", gh.PR{Title: "notify on drift", Author: "owen", Reviewers: []string{"you"}})
	s.AddReview("acme/coast", again.Number, gh.Review{User: "you", State: gh.ReviewCommented})
	if err := c.RequestReviewers(ctx, gh.Repo{Owner: "acme", Name: "coast"}, again.Number, []string{"you"}); err != nil {
		t.Fatal(err)
	}
	said := s.AddPR("acme/unfair", gh.PR{Title: "tidy the README", Author: "bot", Reviewers: []string{"you"}})
	s.AddIssueComment("acme/unfair", said.Number, gh.IssueComment{User: "you", Body: "Looks fine, but the badge is broken."})
	waiting := s.AddPR("acme/lib", gh.PR{Title: "bump deps", Author: "dan", Reviewers: []string{"you"}})
	s.AddIssueComment("acme/lib", waiting.Number, gh.IssueComment{User: "dan", Body: "ready for a look"})

	got, err := c.ReviewQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[int]gh.Asked{}
	for _, a := range got {
		by[a.Number] = a
	}
	if a := by[again.Number]; !a.Requested || a.Mine != gh.ReviewCommented || a.Answered {
		t.Errorf("asked again after commenting: requested %v, mine %q, answered %v", a.Requested, a.Mine, a.Answered)
	}
	if a := by[said.Number]; !a.Requested || !a.Answered {
		t.Errorf("commented in the conversation since asked: requested %v, answered %v", a.Requested, a.Answered)
	}
	if a := by[waiting.Number]; !a.Requested || a.Answered {
		t.Errorf("only the author commented: requested %v, answered %v", a.Requested, a.Answered)
	}
}
