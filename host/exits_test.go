package host

import (
	"context"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// noStart fails if an agent starts within a moment.
func (r *rig) noStart(what string) {
	r.t.Helper()
	select {
	case <-r.fake.Started:
		r.t.Fatalf("an agent started: %s", what)
	case <-time.After(300 * time.Millisecond):
	}
}

// exitsAlone ends a's process as if it ended by itself.
func exitsAlone(a *agenttest.FakeSession) { _ = a.Close() }

// An agent that exits on its own starts again on its conversation.
func TestAnAgentThatExitsStartsAgain(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Session == a.ID() })
	exitsAlone(a)
	r.agent()
	if got := r.fake.Resumed(); len(got) != 1 || got[0] != a.ID() {
		t.Errorf("resumed %v, want %s", got, a.ID())
	}
	r.eventually(id, "the agent running", func(se core.SessionState) bool { return se.Agent.Running })
}

// One that keeps exiting stays stopped, says why, and offers Resume; Resume starts it again.
func TestAnAgentThatKeepsExitingStaysStopped(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	for range 2 {
		exitsAlone(a)
		a = r.agent()
	}
	exitsAlone(a)
	se := r.eventually(id, "the error", func(se core.SessionState) bool { return se.Agent.Error != "" && !se.Agent.Running })
	r.noStart("after the third exit in a row")
	if se.Agent.StoppedBy != core.StoppedByAgent {
		t.Errorf("stopped by %q", se.Agent.StoppedBy)
	}
	var notice core.Notice
	for _, n := range r.w.Store().State().Notifications {
		if n.ID == "agent.exits" && n.Session == id {
			notice = n.Notice
		}
	}
	if len(notice.Actions) != 1 || notice.Actions[0].Command != "session.open" {
		t.Errorf("notice = %+v", notice)
	}
	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	exitsAlone(r.agent())
	r.agent()
}

// Exits far apart never add up to a loop.
func TestExitsFarApartKeepRestarting(t *testing.T) {
	r := newRig(t)
	_, a := r.workSession("Work")
	a.WaitSent(t)
	for range 4 {
		r.setNow(r.now().Add(2 * time.Minute))
		exitsAlone(a)
		a = r.agent()
	}
}

// A stop that's meant, and the option off, start nothing.
func TestNoRestartAfterAStopOrWithTheOptionOff(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	if err := r.w.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.noStart("after a stop")

	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	a = r.agent()
	r.w.SetRestartOnExit(false)
	exitsAlone(a)
	r.eventually(id, "the agent stopped", func(se core.SessionState) bool { return !se.Agent.Running })
	r.noStart("with the option off")
}
