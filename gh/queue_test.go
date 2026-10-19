package gh_test

import (
	"context"
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
