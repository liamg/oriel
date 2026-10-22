package ship

import (
	"cmp"
	"context"
	"fmt"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// mergeStep is ship.merge-default's step.
const mergeStep = "Merging"

// mergeDefault asks first, naming both branches; confirmed, it merges the project's branch into the default branch.
func (x *Ship) mergeDefault(_ registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	s, _ := x.ship(se.ID, p)
	if s.Default == "" {
		return fmt.Errorf("%s has no local default branch to merge into", p)
	}
	if !a.Bool("confirm") {
		x.h.Notify(core.Notice{ID: "ship.merge-default", Severity: core.SevQuestion, Session: se.ID, Title: "Merge into " + s.Default + "?",
			Text: fmt.Sprintf("Merge %s's commits into %s in %s, here, without a PR? %s isn't pushed; uncommitted changes stay on %s.",
				s.Branch, s.Default, p, s.Default, s.Branch),
			Actions: []core.Action{
				{Title: "Merge into " + s.Default, Command: "ship.merge-default", Args: map[string]any{"id": string(se.ID), "project": p, "confirm": true}},
				{Title: "Cancel", Command: "notice.dismiss", Args: map[string]any{"id": "ship.merge-default"}},
			}})
		return nil
	}
	x.h.Dismiss("ship.merge-default")
	return x.step(se.ID, p, mergeStep, func(ctx context.Context, c host.Access) error {
		m, err := c.ShipMergeDefault(ctx, host.ShipRef{Session: se.ID, Project: p})
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

// nextPRStep is ship.next-pr's step.
const nextPRStep = "Starting the next PR"

// nextPR moves a project onto a fresh branch from its updated base, keeping the PR it leaves in the session's past:
// done when given, else the one last read.
func (x *Ship) nextPR(se core.SessionState, p string, done *registry.PR) error {
	s, _ := x.ship(se.ID, p)
	if done == nil {
		done = s.PR
	}
	req := host.NextPRReq{Session: se.ID, Project: p}
	if done != nil {
		req.Done = core.PastPR{Number: done.Number, URL: done.URL, Title: done.Title, State: prStateWord(done)}
		req.Head = done.Head
	}
	x.h.Dismiss(nextPRAsk(p))
	return x.step(se.ID, p, nextPRStep, func(ctx context.Context, c host.Access) error {
		return c.NextPR(ctx, req)
	}, func() { delete(x.prs, formKey(se.ID, p)) })
}

// afterMerge moves a project on to its next PR once Oriel has merged its branch: at once when nothing's in the way
// (the agent idle, nothing uncommitted), else asking.
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
	if ships, _ := x.sessionShips(se); kit.ArchivesOnMerge(x.h, se, projectShips(ships)) {
		return
	}
	if se.Agent.Working || len(s.Files) > 0 {
		x.askNextPR(id, s, done)
		return
	}
	if err := x.nextPR(se, p, done); err != nil {
		x.askNextPR(id, s, done)
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

// askNextPR asks whether to start a project's next PR, its PR having been merged.
func (x *Ship) askNextPR(id core.SessionID, s shipRead, done *registry.PR) {
	w := s.words()
	title := "Merged"
	if done != nil {
		title = fmt.Sprintf("%s%d merged", w.Sigil, done.Number)
	}
	why := ""
	switch {
	case len(s.Files) > 0:
		why = fmt.Sprintf(" %s has uncommitted changes: commit or discard them first.", s.Branch)
	default:
		if sess, ok := x.h.Session(id); ok && sess.Snapshot().Agent.Working {
			why = " The agent is working on it now."
		}
	}
	x.h.Notify(core.Notice{ID: nextPRAsk(s.Project), Severity: core.SevQuestion, Session: id, Title: title,
		Text: fmt.Sprintf("Start the next %s in %s, on a fresh branch from %s?%s", w.Short, s.Project, cmp.Or(s.Base, "its base"), why),
		Actions: []core.Action{
			{Title: "Start next " + w.Short, Command: "ship.next-pr", Args: map[string]any{"id": string(id), "project": s.Project}},
			{Title: "Not now", Command: "notice.dismiss", Args: map[string]any{"id": nextPRAsk(s.Project)}},
		}})
}

// movesOn says a project's PR is done with and its branch has nothing left to raise: its next step is the next PR.
func movesOn(s shipRead) bool {
	return s.PR != nil && !s.PR.Open() && (s.PR.Merged || s.Commits == 0)
}

// nextPRAsk is the id of the notice asking whether to start a project's next PR.
func nextPRAsk(project string) string { return "ship.next-pr." + project }
