package gh_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// What agentbus's review requests read of GitHub (crit's PRSizeOf, ReadPRFacts, Login) and its auto-approval
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

// A review's comments go on the lines of the diff; when one isn't on it, GitHub refuses the review, so every comment
// goes in its body instead.
func TestSubmitReview(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			api := client(t, transport, s).(gh.API)
			ctx := context.Background()
			s.SetLogin("me")
			pr := s.AddPR("acme/api", gh.PR{Title: "fix: rounding", Head: "round", Base: "main"})
			s.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "priya", HeadSHA: "abc123"})
			s.DiffLines("acme/api", pr.Number, "round.go:3")
			on := gh.ReviewComment{Path: "round.go", Line: 3, Body: "Floor, not round."}
			if err := api.SubmitReview(ctx, acme, pr.Number, gh.ReviewInput{Event: "REQUEST_CHANGES", Body: "See the comment.", Commit: "abc123",
				Comments: []gh.ReviewComment{on}}); err != nil {
				t.Fatal(err)
			}
			rs, cs := s.Reviews("acme/api", pr.Number), s.ReviewComments("acme/api", pr.Number)
			if len(rs) != 1 || rs[0].State != gh.ReviewChangesRequested || rs[0].Body != "See the comment." || len(cs) != 1 ||
				cs[0].Path != "round.go" || cs[0].Line != 3 || cs[0].Body != on.Body {
				t.Fatalf("reviews %+v, comments %+v", rs, cs)
			}
			off := gh.ReviewComment{Path: "README.md", Line: 9, Body: "Say so here."}
			if err := api.SubmitReview(ctx, acme, pr.Number, gh.ReviewInput{Event: "COMMENT", Body: "Two things.", Comments: []gh.ReviewComment{on, off}}); err != nil {
				t.Fatal(err)
			}
			rs, cs = s.Reviews("acme/api", pr.Number), s.ReviewComments("acme/api", pr.Number)
			want := "Two things.\n\n**`round.go:3`**\n\nFloor, not round.\n\n**`README.md:9`**\n\nSay so here."
			if len(rs) != 2 || rs[1].Body != want || len(cs) != 1 {
				t.Fatalf("a comment off the diff: reviews %+v, comments %+v", rs, cs)
			}
		})
	}
}

// One comment GitHub won't put on the diff no longer moves every comment into the body: each is placed by the PR's
// patches. A comment in a hunk goes on its line; elsewhere in a changed file, on the file; on a file the PR doesn't
// change, in the body.
func TestSubmitReviewPlacesEachComment(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			api := client(t, transport, s).(gh.API)
			s.SetLogin("me")
			pr := s.AddPR("acme/api", gh.PR{Title: "fix: rounding", Head: "round", Base: "main"})
			patch := "@@ -10,4 +10,5 @@ func Round() {\n ctx := 1\n-old := 2\n+n := 2\n+m := 3\n keep := 4\n end := 5"
			s.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "priya", HeadSHA: "abc123",
				Files: []gh.PRFile{{Path: "round.go", Additions: 2, Deletions: 1, Patch: patch}}})
			inHunk := gh.ReviewComment{Path: "round.go", Line: 11, Body: "Name it."}
			removed := gh.ReviewComment{Path: "round.go", Line: 11, Side: "LEFT", Body: "Why remove it?"}
			ranged := gh.ReviewComment{Path: "round.go", Line: 12, OriginalLine: 3, Body: "From the top."}
			offHunk := gh.ReviewComment{Path: "round.go", Line: 40, Body: "This too."}
			unchanged := gh.ReviewComment{Path: "README.md", Line: 9, Body: "Say so here."}
			err := api.SubmitReview(context.Background(), acme, pr.Number, gh.ReviewInput{Event: "COMMENT", Body: "Some things.", Commit: "abc123",
				Comments: []gh.ReviewComment{inHunk, removed, ranged, offHunk, unchanged}})
			if err != nil {
				t.Fatal(err)
			}
			rs, cs := s.Reviews("acme/api", pr.Number), s.ReviewComments("acme/api", pr.Number)
			if want := "Some things.\n\n**`README.md:9`**\n\nSay so here."; len(rs) != 1 || rs[0].Body != want {
				t.Fatalf("reviews %+v", rs)
			}
			var got []string
			for _, c := range cs {
				got = append(got, fmt.Sprintf("%s:%d:%s %s", c.Path, c.Line, c.Side, c.Body))
			}
			want := []string{"round.go:11:RIGHT Name it.", "round.go:11:LEFT Why remove it?", "round.go:12:RIGHT From the top.",
				"round.go:0:RIGHT **Line 40**\n\nThis too."}
			if !slices.Equal(got, want) {
				t.Errorf("comments\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// GitHub patches are unified diff: a patch's hunks are unified diff, "@@ -old,n +new,m @@" then context, - and + lines.
func TestPatchLines(t *testing.T) {
	l := gh.PatchLines("@@ -1,2 +1,3 @@\n a\n-b\n+c\n+d\n\\ No newline at end of file\n@@ -20 +21,2 @@\n x\n+y")
	for side, want := range map[string][]int{"RIGHT": {1, 2, 3, 21, 22}, "LEFT": {1, 2, 20}} {
		var got []int
		for n := range l[side] {
			got = append(got, n)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", side, got, want)
		}
	}
}
