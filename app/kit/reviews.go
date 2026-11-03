package kit

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
func ReviewedPR(r registry.Registry, se core.SessionState) string {
	if x, ok := registry.ServiceOf[Reviews](r, ReviewsService); ok {
		return x.PR(se)
	}
	return ""
}

// ReviewSession is the session reviewing url, if the window has review sessions.
func ReviewSession(r registry.Registry, url string) (core.Session, bool) {
	if x, ok := registry.ServiceOf[Reviews](r, ReviewsService); ok {
		return x.Session(url)
	}
	return nil, false
}
