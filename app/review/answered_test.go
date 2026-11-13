package review_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/gh"
)

// A pull request you've answered since you were asked (a review of any kind, or a comment in its conversation) is the
// author's move, not yours: it's in progress, and doesn't count toward the pill. Asked again after a review that only
// commented, it's re-requested; and its reviewers list you once, as asked again, not as waiting and as having
// commented both.
func TestAnsweredReviewsAreTheirMove(t *testing.T) {
	s, _, hub, _ := reviewScene(t)
	again := hub.AddPR("acme/coast", gh.PR{Title: "Notify on drift", Author: "owen", Reviewers: []string{"you"}, Head: "drift", Base: "main"})
	hub.AddReview("acme/coast", again.Number, gh.Review{User: "you", State: gh.ReviewCommented})
	if err := hub.Client().RequestReviewers(context.Background(), gh.Repo{Owner: "acme", Name: "coast"}, again.Number, []string{"you"}); err != nil {
		t.Fatal(err)
	}
	said := hub.AddPR("acme/unfair", gh.PR{Title: "Tidy the README", Author: "bot", Reviewers: []string{"you"}})
	hub.AddIssueComment("acme/unfair", said.Number, gh.IssueComment{User: "you", Body: "The badge is broken."})

	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Retry flaky S3") })
	s.Run("reviews.refresh", nil)
	s.Until("both new pull requests listed", func() bool {
		return strings.Contains(s.Text(), "Notify on drift") && strings.Contains(s.Text(), "Tidy the README")
	})
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	inProgress := strings.Index(text, "IN PROGRESS")
	if i := strings.Index(text, "Tidy the README"); i < inProgress {
		t.Errorf("a pull request you commented on since asked waits on you:\n%s", text)
	}
	if i := strings.Index(text, "Notify on drift"); i > inProgress {
		t.Errorf("a pull request you were asked about again isn't waiting on you:\n%s", text)
	}
	if !strings.Contains(text, "Notify on drift now RE-REQUESTED") {
		t.Errorf("asked again after commenting, it isn't re-requested:\n%s", text)
	}
	if n := reviewsOf(s).Count(); n != 4 {
		t.Errorf("the pill counts %d, want 4: the three asked of you or your team, and the one asked again", n)
	}

	s.ClickText("Notify on drift")
	s.Until("its reviewers", func() bool { return s.Find("you re-requested") != nil })
	if n := s.Count("you waiting") + s.Count("you commented"); n != 0 {
		t.Errorf("you're listed %d more times among its reviewers", n)
	}
}
