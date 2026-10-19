package gh_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// PL-5: what agentbus's review requests read of GitHub (crit's PRSizeOf, ReadPRFacts, Login) and its auto-approval
// (crit's ApproveAt: at the head that was read, with no body), over the REST API and through gh.
func TestReviewRequestReads(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			api := client(t, transport, s).(gh.API)
			ctx := context.Background()
			s.SetLogin("me-on-github")
			pr := s.AddPR("acme/api", gh.PR{Title: "chore: bump deps", Head: "deps", Base: "main"})
			s.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "priya", HeadSHA: "abc123", Additions: 12, Deletions: 4,
				Files: []gh.PRFile{{Path: "go.mod"}, {Path: "go.sum", Previous: "go.sum.old"}}})

			if me, err := api.Me(ctx); err != nil || me != "me-on-github" {
				t.Fatalf("Me: %q %v", me, err)
			}
			d, err := api.PRDetails(ctx, acme, pr.Number)
			want := gh.PRDetails{Title: "chore: bump deps", State: "open", Author: "priya", Base: "main", HeadSHA: "abc123", Additions: 12, Deletions: 4,
				ChangedFiles: 2, Head: "deps", URL: pr.URL}
			if err != nil || !reflect.DeepEqual(d, want) {
				t.Fatalf("PRDetails: %+v %v", d, err)
			}
			files, err := api.PRFiles(ctx, acme, pr.Number)
			if err != nil || len(files) != 2 || files[1] != (gh.PRFile{Path: "go.sum", Previous: "go.sum.old"}) {
				t.Fatalf("PRFiles: %+v %v", files, err)
			}
			if err := api.ApproveAt(ctx, acme, pr.Number, "stale"); err == nil {
				t.Error("approved at a commit that isn't the head")
			}
			if err := api.ApproveAt(ctx, acme, pr.Number, "abc123"); err != nil {
				t.Fatal(err)
			}
			rs := s.Reviews("acme/api", pr.Number)
			if len(rs) != 1 || rs[0].User != "me-on-github" || rs[0].State != gh.ReviewApproved || rs[0].Body != "" {
				t.Fatalf("reviews: %+v", rs)
			}
		})
	}
}

func TestGreen(t *testing.T) {
	pass := gh.Check{Name: "test", Status: "completed", Conclusion: "success"}
	skip := gh.Check{Name: "lint", Status: "completed", Conclusion: "neutral"}
	if gh.Green(nil) {
		t.Error("no checks is green")
	}
	if !gh.Green([]gh.Check{pass, skip}) {
		t.Error("passing checks aren't green")
	}
	if gh.Green([]gh.Check{pass, {Name: "e2e", Status: "in_progress"}}) || gh.Green([]gh.Check{pass, {Name: "e2e", Status: "completed", Conclusion: "failure"}}) {
		t.Error("a running or failed check is green")
	}
}

func TestParsePRURL(t *testing.T) {
	r, n, err := gh.ParsePRURL("https://github.com/acme/api/pull/42/files")
	if err != nil || r != acme || n != 42 {
		t.Fatalf("got %v %d %v", r, n, err)
	}
	for _, bad := range []string{"acme/api#42", "https://gitlab.com/acme/api/pull/42", "https://github.com/acme/api/issues/42"} {
		if _, _, err := gh.ParsePRURL(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
