package gh_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// A PR's commits come oldest first, each with its subject and its author's login.
func TestPRCommits(t *testing.T) {
	s := ghtest.New(t)
	pr := s.AddPR("acme/api", gh.PR{Title: "t", Author: "mkhan"})
	s.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "mkhan"})
	s.AddCommit("acme/api", pr.Number, "aaaaaaa1")
	s.AddCommit("acme/api", pr.Number, "bbbbbbb2")
	got, err := s.Client().PRCommits(context.Background(), gh.Repo{Owner: "acme", Name: "api"}, pr.Number)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SHA != "aaaaaaa1" || got[1].SHA != "bbbbbbb2" || got[0].Author != "mkhan" || got[0].Message != "commit aaaaaaa" ||
		got[0].Date.IsZero() {
		t.Errorf("commits: %+v", got)
	}
}
