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

// PRReviewsService gives a PR's reviewers' latest verdicts, as the review mirror last read its threads.
const PRReviewsService = "pr.reviews"

// PRReviews is the PRReviewsService.
type PRReviews interface {
	// Reviews are each reviewer's latest verdict on the PR at url in session id: registry.ReviewChanges,
	// ReviewApproved, ReviewStale or ReviewCommented.
	Reviews(id core.SessionID, url string) map[string]registry.ReviewState
}

// ReviewVerdicts are each reviewer's latest verdict on the PR at url in session id, from whichever plugin keeps them.
func ReviewVerdicts(r registry.Contributions, id core.SessionID, url string) map[string]registry.ReviewState {
	if s, ok := registry.ServiceOf[PRReviews](r, PRReviewsService); ok {
		var out map[string]registry.ReviewState
		registry.IgnorePanic(func() { out = s.Reviews(id, url) })
		return out
	}
	return nil
}
