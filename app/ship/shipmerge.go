package ship

import (
	"context"
	"fmt"

	"github.com/liamg/oriel/app/kit/prui"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/registry"
)

// mergeStep is ship.default_branch.merge's step.
const mergeStep = "Merging"

// mergeDefault asks first, naming both branches; confirmed, it merges the project's branch into the default branch.
func (x *Ship) mergeDefault(_ registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	s, _ := x.ship(se.ID, p)
	if s.Default == "" {
		return fmt.Errorf("%s has no local default branch to merge into", p)
	}
	if !a.Bool("confirm") {
		x.h.Notify(core.Notice{ID: "ship.default_branch.merge", Severity: core.SevQuestion, Confirm: true, Session: se.ID, Title: "Merge into " + s.Default + "?",
			Text: fmt.Sprintf("Merge %s's commits into %s in %s, here, without a PR? %s isn't pushed; uncommitted changes stay on %s.",
				s.Branch, s.Default, p, s.Default, s.Branch),
			Actions: []core.Action{
				{Title: "Merge into " + s.Default, Command: "ship.default_branch.merge", Args: map[string]any{"id": string(se.ID), "project": p, "confirm": true}},
				{Title: "Cancel", Command: "notice.dismiss", Args: map[string]any{"id": "ship.default_branch.merge"}},
			}})
		return nil
	}
	x.h.Dismiss("ship.default_branch.merge")
	return x.step(se.ID, p, mergeStep, func(ctx context.Context, c hostapi.ShipCalls) error {
		m, err := c.ShipMergeDefault(ctx, hostapi.ShipRef{Session: se.ID, Project: p})
		if err != nil {
			return err
		}
		how := "a merge commit"
		if m.FastForward {
			how = "a fast-forward"
		}
		x.h.Notify(core.Notice{ID: "ship.merged-default", Severity: core.SevInfo, Session: se.ID, Title: p,
			Text: fmt.Sprintf("Merged %s into %s (%s), here: push %s when you're ready.", m.From, m.Into, how, m.Into)})
		return nil
	}, func() {
		var done *registry.PR
		if s.PR != nil {
			pr := *s.PR
			pr.Merged = true
			done = &pr
		}
		x.h.Do(func() { x.afterMerge(se.ID, p, done) })
	})
}

// nextPRStep is ship.next_pr.start's step.
const nextPRStep = "Starting the next PR"

// nextPR moves a project onto a fresh branch from its updated base, keeping the PR it leaves in the session's past:
// done when given, else the one last read.
func (x *Ship) nextPR(se core.SessionState, p string, done *registry.PR) error {
	s, _ := x.ship(se.ID, p)
	if done == nil {
		done = s.PR
	}
	req := hostapi.NextPRReq{Session: se.ID, Project: p}
	if done != nil {
		req.Done = core.PastPR{Number: done.Number, URL: done.URL, Title: done.Title, State: done.Ended()}
		req.Head = done.Head
	}
	x.h.Dismiss(nextPRAsk(p))
	return x.step(se.ID, p, nextPRStep, func(ctx context.Context, c hostapi.ShipCalls) error {
		return c.NextPR(ctx, req)
	}, func() { delete(x.prs, formKey(se.ID, p)) })
}

// afterMerge moves a project on to its next PR once its PR is merged, here or on its code host, saying nothing unless
// it can't: at once, or once the agent's turn ends so its branch doesn't move under it. What's on the branch since
// comes along (NextPR). On the loop.
func (x *Ship) afterMerge(id core.SessionID, p string, done *registry.PR) {
	sess, ok := x.h.Session(id)
	if !ok {
		return
	}
	se := sess.Snapshot()
	if !registry.Can(x.h, se.Kind, core.NextPR) {
		return
	}
	s, _ := x.ship(id, p)
	if ships, _ := x.sessionShips(se); prui.ArchivesOnMerge(x.h, se, projectShips(ships)) {
		return
	}
	if se.Agent.Working {
		if x.waiting == nil {
			x.waiting = map[core.SessionID]map[string]*registry.PR{}
		}
		if x.waiting[id] == nil {
			x.waiting[id] = map[string]*registry.PR{}
		}
		x.waiting[id][p] = done
		return
	}
	if err := x.nextPR(se, p, done); err != nil {
		x.cantMoveOn(id, s, err)
	}
}

// turnEnded moves on the projects of session id whose PR merged while its agent was working. On the loop.
func (x *Ship) turnEnded(id core.SessionID) {
	waiting := x.waiting[id]
	delete(x.waiting, id)
	for p, done := range waiting {
		x.afterMerge(id, p, done)
	}
}

// mergedHere marks a project's PR as merged from here, before the read that sees it so: that read asks its code host
// again, so PrMerged fires, and doesn't ask about the next PR, which afterMerge sees to.
func (x *Ship) mergedHere(id core.SessionID, s shipRead, pr registry.PR) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.movedOn == nil {
		x.movedOn = map[string]bool{}
	}
	x.movedOn[pr.URL] = true
	x.cache.mu.Lock()
	delete(x.cache.read, formKey(id, s.Project)+"/"+s.Branch)
	x.cache.mu.Unlock()
}

// cantMoveOn says a project couldn't move on to its next PR by itself, and offers to try again.
func (x *Ship) cantMoveOn(id core.SessionID, s shipRead, err error) {
	w := s.words()
	x.h.Notify(core.Notice{ID: nextPRAsk(s.Project), Severity: core.SevWarning, Session: id, Title: s.Project,
		Text:    fmt.Sprintf("Couldn't start the next %s: %v", w.Short, err),
		Actions: []core.Action{{Title: "Start next " + w.Short, Command: "ship.next_pr.start", Args: map[string]any{"id": string(id), "project": s.Project}}}})
}

// movesOn says a project's PR is done with and its branch has nothing left to raise: its next step is the next PR.
func movesOn(s shipRead) bool {
	return s.PR != nil && !s.PR.Open() && (s.PR.Merged || s.Commits == 0)
}

// nextPRAsk is the id of the notice saying a project couldn't start its next PR by itself.
func nextPRAsk(project string) string { return "ship.next_pr.start." + project }
