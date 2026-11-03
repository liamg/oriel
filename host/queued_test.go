package host

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// A comment still queued behind the agent's turn can be edited in place or deleted, the ones after it kept; one the
// agent has can't; the agent is given what's left, as it now reads.
func TestQueuedCommentsEditAndDelete(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("first")
	a.WaitSent(t)
	tid := r.session(id).Threads[0].ID
	given := r.session(id).Threads[0].Comments[0].ID
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	ctx := context.Background()
	for _, b := range []string{"second", "third", "fourth"} {
		if _, err := r.w.Comment(ctx, CommentReq{Session: id, Thread: tid, Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	th, _ := r.session(id).Thread(tid)
	second, third := th.Comments[1].ID, th.Comments[2].ID
	if err := r.w.EditQueued(QueuedReq{Session: id, Thread: tid, Comment: second, Body: "second, better"}); err != nil {
		t.Fatal(err)
	}
	if err := r.w.EditQueued(QueuedReq{Session: id, Thread: tid, Comment: third, Delete: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.w.EditQueued(QueuedReq{Session: id, Thread: tid, Comment: given, Body: "x"}); err == nil {
		t.Error("a comment the agent has was edited")
	}
	th, _ = r.session(id).Thread(tid)
	var bodies []string
	for _, c := range th.Comments {
		bodies = append(bodies, c.Body)
	}
	if len(bodies) != 3 || bodies[1] != "second, better" || bodies[2] != "fourth" {
		t.Fatalf("comments %q", bodies)
	}
	a.Emit(agent.TurnEnd{})
	got := a.WaitSent(t).Text
	if !containsAll(got, "second, better", "fourth") || containsAll(got, "third") {
		t.Errorf("the agent was given:\n%s", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
