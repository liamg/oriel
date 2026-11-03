//oriel:kind review

package github

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
)

// reviewedPR is the pull request a review session reads; "" for any other session.
func reviewedPR(se core.SessionState) string {
	rd, _ := core.DataOf[core.ReviewData](se)
	return rd.PR
}

// sentFrom is the thread of the review's comment that GitHub's review comment c was posted from: the same words (less
// the line heading one posted on its file), on its file.
func sentFrom(se core.SessionState, c gh.ReviewComment) core.ThreadID {
	rd, _ := core.DataOf[core.ReviewData](se)
	if rd.Draft == nil || c.InReplyTo != 0 {
		return ""
	}
	for id, pk := range rd.Draft.Picks {
		if pk.Sent == "" || strings.TrimSpace(pk.Sent) != gh.PostedAs(c.Body) {
			continue
		}
		if t, ok := se.Thread(core.ThreadID(id)); ok && t.Hosted == nil && t.Anchor != nil && t.Anchor.File == c.Path {
			return t.ID
		}
	}
	return ""
}

// SetReviewDraft keeps the review the user is writing of a review session's pull request.
func (x *GitHub) SetReviewDraft(r core.ReviewDrafted) error {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	if reviewedPR(se) == "" {
		return fmt.Errorf("session %s isn't reviewing a pull request", r.Session)
	}
	return x.st.Emit(r)
}
