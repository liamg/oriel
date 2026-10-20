package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/render/headless"
)

// quitScene is the app on its session host, with a session whose agent is running.
func quitScene(t *testing.T, job *host.Job) (r *running, lw *localHost, a *agenttest.FakeSession) {
	t.Helper()
	lw = newLocalHost(t)
	if job != nil {
		if _, err := lw.w.SaveJob(*job); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "rounding", Prompt: "Round half-even",
		Provider: "fake", Repos: []core.RepoID{core.RepoID(lw.repo)}}); err != nil {
		t.Fatal(err)
	}
	a = lw.fake.WaitStarted(t)
	a.WaitSent(t)
	r = startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the session", func(st core.State) bool { _, ok := st.Session("rounding"); return ok })
	return r, lw, a
}

func (r *running) waitExit() {
	r.t.Helper()
	select {
	case err := <-r.done:
		r.once.Do(func() {})
		if err != nil {
			r.t.Fatalf("the app exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		r.t.Fatal("Oriel didn't quit")
	}
}

// handedOver says whether the host stopped the session's agent for the next start to resume.
func handedOver(lw *localHost) bool {
	se, ok := lw.w.Store().State().Session("rounding")
	return ok && !se.Snapshot().Agent.Running && se.Snapshot().Agent.StoppedBy == core.StoppedByHandOver
}

// sessions.md → As built: quitting Oriel hands its agents over; they come back at the next launch.
func TestQuitHandsTheAgentsOver(t *testing.T) {
	r, lw, _ := quitScene(t, nil)
	r.win.RequestClose()
	r.waitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// With an agent mid-turn, quitting waits for the turn, and Cancel keeps Oriel open with the agent as it was.
func TestQuitWaitsForTheTurnOrCancels(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	waitState(t, r.store, "the turn", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Agent.Working
	})
	for _, cancel := range []func(){func() { r.typeKeys("<Esc>") }, func() { r.clickTop("Cancel") }} {
		r.win.RequestClose()
		r.waitFrame("the quit dialog", func(s string) bool {
			return containsAll(s, "QUITTING ORIEL", "rounding", "mid-turn", "Quit now", "Cancel")
		})
		cancel()
		r.waitGone("QUITTING ORIEL")
		if r.win.Closed() || handedOver(lw) {
			t.Fatal("Cancel quit anyway")
		}
	}
	r.win.RequestClose()
	r.waitText("QUITTING ORIEL")
	a.Emit(agent.TurnEnd{})
	r.waitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// Quit now doesn't wait for the turn; nor does asking to quit again.
func TestQuitNow(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	r.win.RequestClose()
	r.waitText("Quit now")
	r.clickTop("Quit now")
	r.waitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

func TestASecondQuitDoesNotWait(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	r.win.RequestClose()
	r.waitText("QUITTING ORIEL")
	r.win.RequestClose()
	r.waitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// Scheduled jobs warn that they won't run while Oriel is closed, and quits only when told to.
func TestQuitWarnsOfScheduledJobs(t *testing.T) {
	r, _, _ := quitScene(t, &host.Job{Title: "Sweep stale deps", Prompt: "sweep",
		Schedule: host.Schedule{Every: "weekdays", At: "07:00"}})
	r.win.RequestClose()
	r.waitFrame("the warning", func(s string) bool {
		return containsAll(s, "QUITTING ORIEL", "Sweep stale deps", "Quit", "Cancel")
	})
	time.Sleep(300 * time.Millisecond)
	if r.win.Closed() {
		t.Fatal("quit without the warning answered")
	}
	r.clickTop("Quit")
	r.waitExit()
}

// The quit dialog: the sessions mid-turn with the countdown, and the scheduled jobs that won't run.
func TestQuitDialogSnapshot(t *testing.T) {
	s := newErrScene(t, t.TempDir())
	r := &quitDrain{deadline: s.now.Add(42 * time.Second), focus: true, polled: true,
		busy: []core.SessionState{{ID: "rounding", Name: "Round half-even"}},
		jobs: []host.Job{{Name: "sweep", Title: "Sweep stale deps", Schedule: host.Schedule{Every: "weekdays", At: "07:00"}}}}
	s.sh.draining.Store(r)
	for range 3 {
		s.frame()
	}
	s.lint("the quit dialog")
	s.golden("quit")
	if text := s.frameText(); !containsAll(text, "QUITTING ORIEL", "Round half-even", "in 42s", "Sweep stale deps", "Cancel", "Quit now") {
		t.Errorf("the dialog reads:\n%s", text)
	}
}

// Quitting with agents running, none of them mid-turn, still shows what's happening while they stop: the window says
// it's shutting down and lists the agents still stopping, rather than looking frozen. It takes no other keys.
func TestQuitShowsTheShutdown(t *testing.T) {
	r, lw, _ := quitScene(t, nil)
	lw.fake.HoldClose = make(chan struct{})
	r.win.RequestClose()
	r.waitFrame("the shutdown", func(s string) bool {
		return containsAll(s, "SHUTTING DOWN", "Stopping 1 agent", "rounding")
	})
	r.typeKeys("<D-k>")
	time.Sleep(150 * time.Millisecond)
	f := r.latest("the shutdown", func(f headless.Frame) bool { return strings.Contains(f.Text(), "SHUTTING DOWN") })
	if strings.Contains(f.Text(), "Search sessions, commands") {
		t.Error("the omnibox opened over the shutdown")
	}
	close(lw.fake.HoldClose)
	r.waitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}
