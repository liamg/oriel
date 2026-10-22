package gh_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// A PR's review read as Oriel mirrors it (line comments and their replies, reviews and their states, the
// conversation, resolved threads, org membership), and the user's replies posted, over the REST API and through gh.
func TestReviewMirrorReads(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			pr := s.AddPR("acme/api", gh.PR{Title: "feat: round", Head: "pricing-rounding", Base: "main"})
			root := s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER", Path: "round.go", Line: 80,
				Body: "Can this return an error instead?"})
			s.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "jkowalski", InReplyTo: root.ID, Body: "+1"})
			s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewChangesRequested, Body: "A few things."})
			s.AddReview("acme/api", pr.Number, gh.Review{User: "jkowalski", State: gh.ReviewApproved})
			s.AddReview("acme/api", pr.Number, gh.Review{User: "priya-k", State: gh.ReviewCommented, Body: "and a nit"})
			s.AddIssueComment("acme/api", pr.Number, gh.IssueComment{User: "bo", Body: "When does this ship?"})
			s.Resolve(root.ID, true)
			s.AddMember("acme", "priya-k")

			rc, err := c.ReviewComments(ctx, acme, pr.Number)
			if err != nil {
				t.Fatal(err)
			}
			if len(rc) != 2 || rc[0].User != "priya-k" || rc[0].Path != "round.go" || rc[0].Line != 80 || rc[0].Side != "RIGHT" ||
				rc[0].Association != "MEMBER" || rc[1].InReplyTo != root.ID || rc[1].Line != 80 {
				t.Fatalf("review comments %+v", rc)
			}
			rs, err := c.Reviews(ctx, acme, pr.Number)
			if err != nil || len(rs) != 3 {
				t.Fatalf("reviews %+v, %v", rs, err)
			}
			states := gh.ReviewStates(rs)
			if states["priya-k"] != gh.ReviewChangesRequested || states["jkowalski"] != gh.ReviewApproved {
				t.Errorf("a comment after changes requested leaves them requested: %v", states)
			}
			ic, err := c.IssueComments(ctx, acme, pr.Number)
			if err != nil || len(ic) != 1 || ic[0].Body != "When does this ship?" {
				t.Fatalf("conversation %+v, %v", ic, err)
			}
			res, err := c.ResolvedThreads(ctx, acme, pr.Number)
			if err != nil || !res[root.ID] {
				t.Fatalf("resolved %v, %v", res, err)
			}
			if ok, err := c.OrgMember(ctx, "acme", "priya-k"); !ok || err != nil {
				t.Errorf("priya-k in acme: %v, %v", ok, err)
			}
			if ok, err := c.OrgMember(ctx, "acme", "bo"); ok || err != nil {
				t.Errorf("bo in acme: %v, %v", ok, err)
			}

			if err := c.ResolveThread(ctx, acme, pr.Number, root.ID, false); err != nil || s.Resolved(root.ID) {
				t.Fatalf("reopening the thread: %v, resolved %v", err, s.Resolved(root.ID))
			}
			if err := c.ResolveThread(ctx, acme, pr.Number, root.ID, true); err != nil || !s.Resolved(root.ID) {
				t.Fatalf("resolving the thread: %v, resolved %v", err, s.Resolved(root.ID))
			}
			if err := c.ResolveThread(ctx, acme, pr.Number, 999999, true); err == nil {
				t.Error("resolved a thread that isn't there")
			}

			r, err := c.ReplyReview(ctx, acme, pr.Number, root.ID, "Agreed.")
			if err != nil || r.ID == 0 || r.InReplyTo != root.ID || r.User != "you" {
				t.Fatalf("reply %+v, %v", r, err)
			}
			ic2, err := c.Comment(ctx, acme, pr.Number, "Next week.")
			if err != nil || ic2.ID == 0 || ic2.User != "you" {
				t.Fatalf("comment %+v, %v", ic2, err)
			}
			if n := len(s.ReviewComments("acme/api", pr.Number)); n != 3 {
				t.Errorf("%d review comments after the reply", n)
			}
		})
	}
}

func TestReviewStatesDismissed(t *testing.T) {
	got := gh.ReviewStates([]gh.Review{{User: "a", State: gh.ReviewChangesRequested}, {User: "a", State: gh.ReviewDismissed}, {User: "b", State: gh.ReviewCommented}})
	if _, ok := got["a"]; ok || got["b"] != gh.ReviewCommented {
		t.Errorf("states %v", got)
	}
}

// The agent never posts to a review: what the guards recognise as posting, and what they let through.
func TestPostsReview(t *testing.T) {
	for cmd, want := range map[string]bool{
		`gh pr review 12 --approve`:                                                     true,
		`gh pr comment 12 --body "looks good"`:                                          true,
		`cd x && gh issue comment 12 -b hi`:                                             true,
		`gh pr close 12 --comment "superseded"`:                                         true,
		`gh api repos/acme/api/pulls/12/comments/34/replies -f body=Agreed`:             true,
		`gh api --method POST /repos/{owner}/{repo}/issues/12/comments -f body=x`:       true,
		`gh api -XPATCH repos/acme/api/pulls/comments/9 -f body=x`:                      true,
		`gh api repos/acme/api/pulls/12/reviews --input review.json`:                    true,
		`/usr/local/bin/gh api repos/acme/api/pulls/12/reviews -F event=APPROVE`:        true,
		`gh api graphql -f query='mutation { addPullRequestReviewComment(input: {}) }'`: true,
		`sh -c 'gh pr comment 1 -b x'`:                                                  true,
		`echo $(gh api repos/a/b/issues/1/comments -f body=x)`:                          true,
		`gh api repos/acme/api/pulls/12/comments`:                                       false,
		`gh api --method GET repos/acme/api/pulls/12/reviews -f per_page=100`:           false,
		`gh api graphql -f query='query { viewer { login } }'`:                          false,
		`gh pr view 12 --comments`:                                                      false,
		`gh pr create --title x`:                                                        false,
		`gh api repos/acme/api/git/refs -f ref=refs/tags/v1 -f sha=abc`:                 false,
		`echo "gh pr comment"`:                                                          false,
		`git commit -m "gh pr review"`:                                                  false,
	} {
		if got := gh.PostsReview(cmd); got != want {
			t.Errorf("PostsReview(%s) = %v, want %v", cmd, got, want)
		}
	}
	for name, want := range map[string]bool{
		"mcp__github__add_reply_to_pull_request_comment":   true,
		"github__add_issue_comment":                        true,
		"mcp__oriel-gw__github__pull_request_review_write": true,
		"github__pull_request_read":                        false,
		"mcp__oriel__reply":                                false,
	} {
		if got := gh.ReviewTool(name); got != want {
			t.Errorf("ReviewTool(%s) = %v, want %v", name, got, want)
		}
	}
}
