package services

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// ReviewsService is the review sessions' pull requests, as the review kind keeps them.
const ReviewsService = "reviews"

// Reviews are the review sessions' pull requests.
type Reviews interface {
	// PR is the pull request se reviews; "" for any other session.
	PR(se core.SessionState) string
	// Session is the session reviewing url: a live one before an archived one, then the newest.
	Session(url string) (core.Session, bool)
}

// ReviewedPR is the pull request se reviews, if the window has review sessions; "" for any other session.
func ReviewedPR(r registry.Contributions, se core.SessionState) string {
	if x, ok := registry.ServiceOf[Reviews](r, ReviewsService); ok {
		return x.PR(se)
	}
	return ""
}

// ReviewSession is the session reviewing url, if the window has review sessions.
func ReviewSession(r registry.Contributions, url string) (core.Session, bool) {
	if x, ok := registry.ServiceOf[Reviews](r, ReviewsService); ok {
		return x.Session(url)
	}
	return nil, false
}

// PRReviewsService gives a PR's reviewers' latest verdicts, for a code host that keeps them (GitHub's mirror).
const PRReviewsService = "pr.reviews"

// PRReviews is the PRReviewsService.
type PRReviews interface {
	// Reviews are the latest verdict of each reviewer of the PR at url in session id, as the host says it.
	Reviews(id core.SessionID, url string) []string
}

// PRReviewsOf are the reviewers' latest verdicts on the PR at url in session id, from whichever plugin keeps them.
func PRReviewsOf(r registry.Contributions, id core.SessionID, url string) []string {
	if s, ok := registry.ServiceOf[PRReviews](r, PRReviewsService); ok {
		var out []string
		registry.Call(func() error { out = s.Reviews(id, url); return nil })
		return out
	}
	return nil
}
