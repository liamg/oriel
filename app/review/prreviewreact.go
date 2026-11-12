package review

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

const optStartReaction = "review.start_reaction"

// reactOnStart reacts to a review session's pull request on its host, so its author sees it's being read.
func (x *PRReview) reactOnStart(id core.SessionID) {
	x.startReaction(id, "react to", registry.ReactToPR, func(s registry.HostSite) bool { return s.React != nil })
}

// unreactOnSent takes the start reaction back once the user's review is sent: the review itself says it was read.
func (x *PRReview) unreactOnSent(id core.SessionID) {
	x.startReaction(id, "take the reaction back from", registry.UnreactToPR, func(s registry.HostSite) bool { return s.Unreact != nil })
}

// startReaction calls do with review.start_reaction on id's pull request off the loop, when it's set and the PR's host
// can (has).
func (x *PRReview) startReaction(id core.SessionID, what string, do func(context.Context, registry.HostSite, string, string) error,
	has func(registry.HostSite) bool) {
	emoji, _, _ := x.h.Options().Get(optStartReaction, registry.At{})
	e, _ := emoji.(string)
	se, ok := x.h.Session(id)
	if e == "" || !ok {
		return
	}
	st := se.Snapshot()
	url := reviewPR(st)
	src, ok := x.site(st)
	if url == "" || !ok || !has(src) {
		return
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := do(ctx, src, url, e); err != nil {
			x.h.Log().Warn("review: couldn't "+what+" "+url, "err", err)
		}
	})
}
