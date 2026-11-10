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
	emoji, _, _ := x.h.Options().Get(optStartReaction, registry.At{})
	e, _ := emoji.(string)
	se, ok := x.h.Session(id)
	if e == "" || !ok {
		return
	}
	st := se.Snapshot()
	url := reviewPR(st)
	src, ok := x.site(st)
	if url == "" || !ok || src.React == nil {
		return
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := registry.ReactToPR(ctx, src, url, e); err != nil {
			x.h.Log().Warn("review: couldn't react to "+url, "err", err)
		}
	})
}
