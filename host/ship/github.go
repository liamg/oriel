package ship

import (
	"sync"

	"github.com/liamg/oriel/host/hostapi"
)

// Review mirroring: a session's PRs' review threads and conversation become threads with origin codehost, which
// reach the agent like any comment. On them the agent only drafts; the user forwards a draft, and its code host posts
// it as the user. A forwarded comment carries its id there, so it never comes back as a new comment (echo
// reconciliation); edits, deletions and resolutions there update the mirror.

type ForwardReq = hostapi.ForwardReq

type DraftEditReq = hostapi.DraftEditReq

// mirrorState serialises applying a mirror with a forward's post and mark, so a forward's echo is always recognised.
type mirrorState struct {
	mu sync.Mutex
}
