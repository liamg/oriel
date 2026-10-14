package core

import (
	"slices"
	"testing"
)

// ui.md 05: "Readiness: Start work unlocks when every thread is resolved or deferred." The plan's own thread, where
// the prompt and Start work are said, and side questions don't hold it up (sessions.md, As built: "Start work makes
// the worktrees ... once every thread is resolved or deferred").
func TestWhatHoldsUpStartWork(t *testing.T) {
	doc := &Anchor{Project: DocsProject, File: "PLAN.md", Line: 14}
	se := SessionState{StartWork: true, Threads: []Thread{
		{ID: "plan", Status: ThreadOpen, Comments: []Comment{{Author: AuthorYou, Body: "Round invoice lines half-to-even"}}},
		{ID: "open", Anchor: doc, Status: ThreadOpen, Comments: []Comment{{Author: AuthorYou, Body: "half-up?"}}},
		{ID: "resolved", Anchor: doc, Status: ThreadResolved, Comments: []Comment{{Author: AuthorYou, Body: "precision?"}}},
		{ID: "deferred", Anchor: doc, Status: ThreadDeferred, Comments: []Comment{{Author: AuthorYou, Body: "later"}}},
		{ID: "btw", Status: ThreadOpen, Btw: true, Comments: []Comment{{Author: AuthorYou, Body: "why decimal?", Btw: true}}},
		{ID: "general", Status: ThreadOpen, Comments: []Comment{{Author: AuthorAgent, Body: "Which currencies?"}}},
	}}
	if got := se.PlanThread(); got != "plan" {
		t.Errorf("plan thread = %q", got)
	}
	var ids []ThreadID
	for _, th := range se.Holding() {
		ids = append(ids, th.ID)
	}
	if !slices.Equal(ids, []ThreadID{"open", "general"}) {
		t.Errorf("holding = %v", ids)
	}
	if !se.Planning() {
		t.Error("a plan before Start work isn't planning")
	}
	se.Started = true
	if se.Planning() {
		t.Error("a plan whose work started is still planning")
	}
	if (SessionState{Kind: "work"}).Planning() {
		t.Error("a work session is planning")
	}
}

// Start work clears a plan's threads (sessions.md, Plan → Start work): ThreadsRemoved takes them out of the state.
func TestThreadsRemoved(t *testing.T) {
	s := &State{Sessions: []SessionState{{ID: "s", Threads: []Thread{{ID: "a"}, {ID: "b"}, {ID: "c"}}}}}
	if err := (ThreadsRemoved{Session: "s", Threads: []ThreadID{"a", "c"}}).Apply(s); err != nil {
		t.Fatal(err)
	}
	if th := s.Sessions[0].Threads; len(th) != 1 || th[0].ID != "b" {
		t.Errorf("threads = %+v", th)
	}
}
