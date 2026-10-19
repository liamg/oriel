package registry_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// ui.md, "Reviews page": "new: asked, no review from you yet. Counts." "re-requested … Counts." "team ask: counts
// unless reviews.count_teams is off." "changes requested / commented … Doesn't count" "draft: listed last, never counts."
func TestReviewCounts(t *testing.T) {
	for _, c := range []struct {
		r            registry.ReviewRequest
		teams, count bool
	}{
		{registry.ReviewRequest{}, true, true},
		{registry.ReviewRequest{State: registry.ReviewNew}, false, true},
		{registry.ReviewRequest{State: registry.ReviewReRequested}, false, true},
		{registry.ReviewRequest{State: registry.ReviewNew, Via: "@acme/platform"}, true, true},
		{registry.ReviewRequest{State: registry.ReviewNew, Via: "@acme/platform"}, false, false},
		{registry.ReviewRequest{State: registry.ReviewChanges}, true, false},
		{registry.ReviewRequest{State: registry.ReviewCommented}, true, false},
		{registry.ReviewRequest{State: registry.ReviewNew, Draft: true}, true, false},
	} {
		if got := c.r.Counts(c.teams); got != c.count {
			t.Errorf("%+v teams=%v: counts %v, want %v", c.r, c.teams, got, c.count)
		}
	}
}

// ui.md, "Reviews page": "Groups: Waiting on you (oldest ask first), then In progress." "draft: listed last".
func TestSortReviews(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 19, h, 0, 0, 0, time.UTC) }
	rs := []registry.ReviewRequest{
		{Number: 1, State: registry.ReviewChanges, Asked: at(1)},
		{Number: 2, State: registry.ReviewNew, Asked: at(9)},
		{Number: 3, State: registry.ReviewNew, Asked: at(3), Draft: true},
		{Number: 4, State: registry.ReviewReRequested, Asked: at(5)},
		{Number: 5, State: registry.ReviewCommented, Asked: at(4)},
	}
	registry.SortReviews(rs)
	var got []int
	for _, r := range rs {
		got = append(got, r.Number)
	}
	want := []int{4, 2, 3, 5, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// hosts.md: a host's site is found by hostname, the repo by the remote's path, a PR by its URL's path;
// RequestedReviews fills in Source and State, and a panicking host is an error.
func TestCodeHostSites(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.CodeHost{Name: "github", Title: "GitHub", Order: 10, Sites: []registry.Site{{Host: "github.com"}},
		Path: regexp.MustCompile(`^/?([^/]+/[^/]+?)(?:\.git)?/?$`), Takes: regexp.MustCompile(`^/[^/]+/[^/]+/pull/\d+`),
		Requested: func(context.Context, registry.Site) ([]registry.ReviewRequest, error) {
			return []registry.ReviewRequest{{Repo: "acme/api", Number: 812}}, nil
		}})
	r.Contribute(registry.CodeHost{Name: "gitlab", Order: 20, Setup: registry.SiteSetup{API: "https://{host}/api/v4", Secret: "GITLAB_TOKEN"},
		Requested: func(context.Context, registry.Site) ([]registry.ReviewRequest, error) { panic("down") }})
	for remote, want := range map[string][2]string{
		"https://github.com/acme/api.git":      {"github.com", "/acme/api.git"},
		"git@github.com:acme/api.git":          {"github.com", "/acme/api.git"},
		"ssh://git@git.acme.corp:2222/a/b.git": {"git.acme.corp", "/a/b.git"},
	} {
		if h, p, ok := registry.ParseRemote(remote); !ok || [2]string{h, p} != want {
			t.Errorf("%s: %s %s %v", remote, h, p, ok)
		}
	}
	gh, _ := registry.HostByName(r, "github")
	if repo, ok := gh.RepoAt("/acme/api.git"); !ok || repo != "acme/api" {
		t.Errorf("repo %q %v", repo, ok)
	}
	if !gh.TakesPath("/acme/api/pull/812") || gh.TakesPath("/acme/api/issues/812") {
		t.Error("TakesPath")
	}
	gl, _ := registry.HostByName(r, "gitlab")
	if s := gl.Setup.For("git.acme.corp", ""); s.API != "https://git.acme.corp/api/v4" || s.Secret != "GITLAB_TOKEN:git.acme.corp" {
		t.Errorf("a self-hosted site %+v", s)
	}
	site, _ := gh.PublicSite("github.com")
	got, err := registry.RequestedReviews(context.Background(), registry.HostSite{CodeHost: gh, Site: site})
	if err != nil || len(got) != 1 || got[0].Source != "GitHub" || got[0].State != registry.ReviewNew || got[0].Key() != "acme/api#812" {
		t.Errorf("got %+v, %v", got, err)
	}
	if l := (registry.HostSite{CodeHost: gl, Site: gl.Setup.For("git.acme.corp", "")}).Label(); l != "gitlab (git.acme.corp)" {
		t.Errorf("a self-hosted site's label %q", l)
	}
	var p *registry.Panic
	if _, err := registry.RequestedReviews(context.Background(), registry.HostSite{CodeHost: gl}); !errors.As(err, &p) {
		t.Errorf("a panicking host: %v", err)
	}
}
