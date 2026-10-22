//oriel:kind review

package aireview

import "github.com/liamg/oriel/core"

// reviewIntro is how a review session's AI review is asked, for its pull request.
func reviewIntro(se core.SessionState) (string, bool) {
	rd, ok := core.DataOf[core.ReviewData](se)
	if !ok {
		return "", false
	}
	if rd.PR != "" {
		return "Review this pull request (" + rd.PR + ").\n\n", true
	}
	return "Review this pull request.\n\n", true
}
