//oriel:kind review

package ship

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
)

// reviewedPR is the pull request a review session reads; "" for any other session.
func reviewedPR(se core.SessionState) string {
	rd, _ := core.DataOf[core.ReviewData](se)
	return rd.PR
}

// sentFrom is the thread of the review's comment that a code host's comment on path was posted from: the same
// words, as written, on the same file.
func sentFrom(se core.SessionState, path, written string) core.ThreadID {
	rd, _ := core.DataOf[core.ReviewData](se)
	if rd.Draft == nil {
		return ""
	}
	for id, pk := range rd.Draft.Picks {
		if pk.Sent == "" || strings.TrimSpace(pk.Sent) != strings.TrimSpace(written) {
			continue
		}
		if t, ok := se.Thread(core.ThreadID(id)); ok && t.Hosted == nil && t.Anchor != nil && t.Anchor.File == path {
			return t.ID
		}
	}
	return ""
}

// SetReviewDraft keeps the review the user is writing of a review session's pull request.
func (x *Ships) SetReviewDraft(r core.ReviewDrafted) (err error) {
	defer x.env.Recover("SetReviewDraft", &err)
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	if reviewedPR(se) == "" {
		return fmt.Errorf("session %s isn't reviewing a pull request", r.Session)
	}
	return x.st.Emit(r)
}
