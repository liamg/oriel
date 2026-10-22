package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

func (x *PRReview) fetch(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	w, err := kit.SessionHost(x.h)
	if err != nil {
		return err
	}
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.fetch." + string(se.ID), Doing: "Fetching " + kit.Label(se)},
		func(ctx context.Context) (map[string]host.PRMove, error) { return w.UpdatePR(ctx, se.ID) },
		func(_ registry.Ctx, moved map[string]host.PRMove) error {
			x.forget(reviewPR(se))
			for _, m := range moved {
				x.form(se.ID).moved = m // the tab's banner says so
			}
			if len(moved) == 0 {
				x.h.Notify(core.Notice{ID: "review.pr.fetch", Severity: core.SevInfo, Text: kit.Label(se) + " has nothing new."})
			}
			return nil
		})
}

// pushed is a sync's word (kit.PrPushed) that a review's pull request is at head on its host: when that's new it's fetched, and with
// review.auto_rereview on, the agent is asked to review what changed once its last review is done.
func (x *PRReview) pushed(id core.SessionID, head string) {
	ref, ok := x.h.Session(id)
	if !ok {
		return
	}
	se := ref.Snapshot()
	if reviewPR(se) != "" {
		x.refresh(reviewPR(se))
	}
	p, ok := prProject(se)
	if reviewPR(se) == "" || !ok || head == "" || p.Commit == "" || head == p.Commit || se.Archived || x.ended(reviewPR(se)) {
		return
	}
	if on, _, _ := x.h.Options().Get(optAutoRereview, registry.At{}); on != true {
		x.forget(reviewPR(se)) // the tab's bar offers Fetch latest
		return
	}
	x.mu.Lock()
	if x.fetching[id] {
		x.mu.Unlock()
		return
	}
	x.fetching[id] = true
	x.mu.Unlock()
	w, err := kit.SessionHost(x.h)
	if err != nil {
		return
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		moved, err := w.UpdatePR(ctx, id)
		cancel()
		x.mu.Lock()
		delete(x.fetching, id)
		x.mu.Unlock()
		x.h.Do(func() {
			if err != nil {
				x.h.Notify(core.Notice{ID: "review.pr.fetch", Severity: core.SevWarning, Session: id, Text: "Fetching " + kit.Label(se) + ": " + err.Error()})
				return
			}
			x.forget(reviewPR(se))
			for _, m := range moved {
				x.form(id).moved = m
				text := fmt.Sprintf("%s has %s.", kit.Label(se), kit.Counted(m.Commits, "new commit"))
				if t, ok := reviewRequest(se); ok && !reviewing(se, t) {
					x.run("review.pr.recheck", registry.Args{"id": string(id)})
					text += " The agent is reviewing them."
				}
				x.h.Notify(core.Notice{ID: "review.pr.pushed." + string(id), Severity: core.SevInfo, Session: id, Title: "New commits", Text: text,
					Actions: []core.Action{openReview(id)}})
			}
		})
	}()
}

// recheck asks the agent to review the commits a fetch brought: the review pack's later round, with where each of
// its earlier comments stands and what the user made of the rest.
func (x *PRReview) recheck(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	m := x.form(se.ID).moved
	if m.To == "" {
		return errors.New("no fetch has brought new commits")
	}
	x.form(se.ID).moved = host.PRMove{}
	var b strings.Builder
	fmt.Fprintf(&b, "The PR was reviewed before: the author has pushed %s since you reviewed %s. Review what changed (`git diff %s %s`) as a later round.",
		kit.Counted(m.Commits, "commit"), kit.Label(se), kit.ShortSHA(m.From), kit.ShortSHA(m.To))
	if sent := sentItems(se); len(sent) > 0 {
		b.WriteString("\n\nYour earlier comments, as posted:\n")
		for _, s := range sent {
			fmt.Fprintf(&b, "- `%s:%d` (%s, thread %s): %s\n", s.thread.Anchor.File, s.thread.Anchor.Line, sentState(s), s.thread.ID, firstWords(s.posted))
		}
	}
	var dropped, rated []string
	for _, it := range x.items(se) {
		if it.pick.State == core.PickDrop {
			dropped = append(dropped, fmt.Sprintf("- `%s:%d`: %s", it.at.File, it.at.Line, it.title))
		}
		if it.pick.Severity != "" {
			rated = append(rated, fmt.Sprintf("- `%s:%d`: %s → %s", it.at.File, it.at.Line, it.sev, it.pick.Severity))
		}
	}
	if len(dropped) > 0 {
		b.WriteString("\nThe user dropped:\n" + strings.Join(dropped, "\n") + "\n")
	}
	if len(rated) > 0 {
		b.WriteString("\nThe user re-rated:\n" + strings.Join(rated, "\n") + "\n")
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"id": string(se.ID), "title": "Review the new commits", "body": b.String()})
}
