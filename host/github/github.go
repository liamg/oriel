package github

import (
	"sync"

	"github.com/liamg/oriel/host/hostapi"
)

// GitHub review mirroring: a session's PRs' review threads and conversation become threads with
// origin github, which reach the agent like any comment. On them the agent only drafts; the user forwards a draft,
// and the host posts it with the user's own GitHub login. A forwarded comment carries its GitHub ID, so it never
// comes back as a new comment (echo reconciliation); edits, deletions and resolutions on GitHub update the mirror.

type Trust = hostapi.Trust

type MirrorReq = hostapi.MirrorReq

type Mirror = hostapi.Mirror

type MirrorPR = hostapi.MirrorPR

type ForwardReq = hostapi.ForwardReq

type DraftEditReq = hostapi.DraftEditReq

// mirrorState serialises applying a mirror with a forward's post and mark, so a forward's echo is always recognised;
// and remembers org members.
type mirrorState struct {
	mu        sync.Mutex
	membersMu sync.Mutex
	members   map[string]bool // org/login
	mu2       sync.Mutex
	me        string // the user's GitHub login
}
