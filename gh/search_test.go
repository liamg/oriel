package gh_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// crit-reference.md, "PR lists": "Mine and review-requested". GitHub's issue search finds them across repositories
// (author:@me, review-requested:@me), the most recently updated first.
func TestSearchPRs(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			s.AddPR("acme/api", gh.PR{Title: "retry transient 502s", Author: "maria", Reviewers: []string{"you"}})
			s.AddPR("acme/api", gh.PR{Title: "round half to even", Draft: true})
			s.AddPR("acme/web", gh.PR{Title: "old one", State: "closed"})
			s.AddPR("acme/web", gh.PR{Title: "dark mode"})
			mine, err := c.SearchPRs(context.Background(), gh.SearchMine)
			if err != nil {
				t.Fatal(err)
			}
			if len(mine) != 2 || mine[0].Title != "dark mode" || mine[0].Repo != "acme/web" || mine[1].Title != "round half to even" ||
				!mine[1].Draft || mine[1].Author != "you" || mine[1].URL == "" || mine[1].Number == 0 {
				t.Errorf("mine: %+v", mine)
			}
			review, err := c.SearchPRs(context.Background(), gh.SearchReview)
			if err != nil {
				t.Fatal(err)
			}
			if len(review) != 1 || review[0].Title != "retry transient 502s" || review[0].Author != "maria" || review[0].Repo != "acme/api" {
				t.Errorf("review requests: %+v", review)
			}
		})
	}
}

func TestParsePR(t *testing.T) {
	for ref, want := range map[string]string{
		"https://github.com/acme/acme/pull/4410":   "https://github.com/acme/acme/pull/4410",
		"https://github.com/acme/api/pull/7/files": "https://github.com/acme/api/pull/7",
		"git@github.com:acme/api.git/pull/7":       "https://github.com/acme/api/pull/7",
		" acme/api#12 ":                            "https://github.com/acme/api/pull/12",
		"github.com/acme/web.site/pull/3":          "https://github.com/acme/web.site/pull/3",
	} {
		r, n, err := gh.ParsePR(ref)
		if err != nil || gh.PRURL(r, n) != want {
			t.Errorf("%q: %v #%d %v, want %s", ref, r, n, err, want)
		}
	}
	for _, bad := range []string{"", "4410", "acme/api", "https://gitlab.com/a/b/-/merge_requests/1"} {
		if _, _, err := gh.ParsePR(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
