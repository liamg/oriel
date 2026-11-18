package ship_test

import (
	"strings"
	"testing"
	"time"

	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/registry"
)

// A check that failed and was run again on the same commit, passing, is that one check, passed: the Pull request
// panel's ↻ shows it so at once, and the PR tab lists it once, though GitHub won't say which workflow each run was
// of and so lists both runs.
func TestACheckRunAgainThatPassesShowsOncePassed(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	branch := ""
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	hub.Fail("GET /repos/acme/search-api/actions/runs", &gh.Error{Status: 403, Message: "Resource not accessible by integration"})
	t0 := time.Now().Add(-10 * time.Minute)
	failed := gh.Check{ID: 31, Name: "unit", Status: "completed", Conclusion: "failure", Actions: true, Suite: 7, Started: t0, Completed: t0.Add(time.Minute)}
	lint := gh.Check{ID: 30, Name: "lint", Status: "completed", Conclusion: "success", Actions: true, Suite: 7, Started: t0, Completed: t0.Add(time.Minute)}
	hub.SetChecks("acme/search-api", branch, lint, failed)
	s.Run("project.open", registry.Args{"project": "search-api"})
	s.Run("project.panel.toggle", registry.Args{"name": "pull-request"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the failing check", func() bool { return strings.Contains(s.Text(), "1 of 2 failing") })

	// Run again on GitHub, on the same commit: a new run of unit beside the failed one, passing.
	t1 := t0.Add(5 * time.Minute)
	hub.SetChecks("acme/search-api", branch, lint, failed,
		gh.Check{ID: 32, Name: "unit", Status: "completed", Conclusion: "success", Actions: true, Suite: 8, Started: t1, Completed: t1.Add(time.Minute)})
	s.ClickOn("Read again: fetch origin, ask GitHub")
	shipSettle(s, t, "the check passing", func() bool { return strings.Contains(s.Text(), "All checks passed (2)") })
	if strings.Contains(s.Text(), "failing") {
		t.Errorf("the panel still says failing after the run again passed:\n%s", s.Text())
	}

	s.ClickOn("View PR")
	s.Until("the PR tab's checks", func() bool { return strings.Contains(s.Text(), "CHECKS · 2 OF 2") })
	if n := strings.Count(s.Text(), "\nunit\n"); n != 1 {
		t.Errorf("the PR tab lists unit %d times:\n%s", n, s.Text())
	}
}
