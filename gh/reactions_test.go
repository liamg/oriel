package gh_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// Taking a reaction back deletes the signed-in user's own, found among the PR's reactions of that kind, and leaves
// everyone else's; not having one is no error.
func TestUnreactTakesBackTheUsersOwn(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			repo := gh.Repo{Owner: "acme", Name: "api"}
			pr := s.AddPR("acme/api", gh.PR{Title: "back off on 429", Head: "eng-2231", Base: "main"})
			if err := c.UnreactPR(ctx, repo, pr.Number, "eyes"); err != nil {
				t.Fatalf("taking back a reaction the user hasn't: %v", err)
			}
			if err := c.ReactPR(ctx, repo, pr.Number, "eyes"); err != nil {
				t.Fatal(err)
			}
			s.SetPRReactions("acme/api", pr.Number, gh.Reactions{"eyes": 3, "rocket": 1})
			if err := c.UnreactPR(ctx, repo, pr.Number, "eyes"); err != nil {
				t.Fatal(err)
			}
			if got := s.PRReactions("acme/api", pr.Number); got["eyes"] != 2 || got["rocket"] != 1 {
				t.Errorf("reactions after taking the user's 👀 back: %v", got)
			}
		})
	}
}
