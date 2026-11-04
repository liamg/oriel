package shell_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/render/headless"
)

// quitScene is the app on its session host, with a session whose agent is running.
func quitScene(t *testing.T, job *host.Job, change ...func(*apptest.Config)) (r *apptest.Window, lw *rig.Host, a *agenttest.FakeSession) {
	t.Helper()
	lw = rig.NewHost(t)
	if job != nil {
		if _, err := lw.W.SaveJob(*job); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "rounding", Prompt: "Round half-even",
		Provider: "fake", Repos: []core.RepoID{core.RepoID(lw.Repo)}}); err != nil {
		t.Fatal(err)
	}
	a = lw.Fake.WaitStarted(t)
	a.WaitSent(t)
	connect := apptest.Connect(lw.W)
	r = apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: func(cfg *apptest.Config) {
		connect(cfg)
		for _, f := range change {
			f(cfg)
		}
	}})
	rig.WaitState(t, r.Store, "the session", func(st core.State) bool { _, ok := st.Session("rounding"); return ok })
	return r, lw, a
}

// handedOver says whether the host stopped the session's agent for the next start to resume.
func handedOver(lw *rig.Host) bool {
	se, ok := lw.W.Store().State().Session("rounding")
	return ok && !se.Snapshot().Agent.Running && se.Snapshot().Agent.StoppedBy == core.StoppedByHandOver
}

// Quitting Oriel hands its agents over; they come back at the next launch.
func TestQuitHandsTheAgentsOver(t *testing.T) {
	r, lw, _ := quitScene(t, nil)
	r.Win.RequestClose()
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// The quit command quits as the close button does.
func TestTheQuitCommandHandsTheAgentsOver(t *testing.T) {
	r, lw, _ := quitScene(t, nil)
	r.Run("quit", nil)
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// A quit from outside the window, a signal's, hands the agents over without waiting for scheduled jobs' Quit.
func TestAQuitFromOutsideDoesNotAskAboutJobs(t *testing.T) {
	quit := make(chan struct{}, 1)
	r, lw, _ := quitScene(t, &host.Job{Title: "Sweep stale deps", Prompt: "sweep", Schedule: host.Schedule{Every: "weekdays", At: "07:00"}},
		func(cfg *apptest.Config) { cfg.Quit = quit })
	quit <- struct{}{}
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// With an agent mid-turn, quitting waits for the turn, and Cancel keeps Oriel open with the agent as it was.
func TestQuitWaitsForTheTurnOrCancels(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	rig.WaitState(t, r.Store, "the turn", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Agent.Working
	})
	for _, cancel := range []func(){func() { r.TypeKeys("<Esc>") }, func() { r.ClickTop("Cancel") }} {
		r.Win.RequestClose()
		r.WaitFrame("the quit dialog", func(s string) bool {
			return apptest.HasAll(s, "QUITTING ORIEL", "rounding", "mid-turn", "Quit now", "Cancel")
		})
		cancel()
		r.WaitGone("QUITTING ORIEL")
		if r.Win.Closed() || handedOver(lw) {
			t.Fatal("Cancel quit anyway")
		}
	}
	r.Win.RequestClose()
	r.WaitText("QUITTING ORIEL")
	a.Emit(agent.TurnEnd{})
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// Quit now doesn't wait for the turn; nor does asking to quit again.
// working waits for the agent's turn to be under way, which is what quitting waits for.
func working(r *apptest.Window) {
	r.T.Helper()
	r.UntilState("the agent working", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Agent.Working
	})
}

func TestQuitNow(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	working(r)
	r.Win.RequestClose()
	r.WaitText("Quit now")
	r.ClickTop("Quit now")
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

func TestASecondQuitDoesNotWait(t *testing.T) {
	r, lw, a := quitScene(t, nil)
	a.Emit(agent.TurnStart{})
	working(r)
	r.Win.RequestClose()
	r.WaitText("QUITTING ORIEL")
	r.Win.RequestClose()
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}

// Scheduled jobs warn that they won't run while Oriel is closed, and quits only when told to.
func TestQuitWarnsOfScheduledJobs(t *testing.T) {
	r, _, _ := quitScene(t, &host.Job{Title: "Sweep stale deps", Prompt: "sweep",
		Schedule: host.Schedule{Every: "weekdays", At: "07:00"}})
	r.Win.RequestClose()
	r.WaitFrame("the warning", func(s string) bool {
		return apptest.HasAll(s, "QUITTING ORIEL", "Sweep stale deps", "Quit", "Cancel")
	})
	time.Sleep(300 * time.Millisecond)
	if r.Win.Closed() {
		t.Fatal("quit without the warning answered")
	}
	r.ClickTop("Quit")
	r.WaitExit()
}

// The quit dialog: the sessions mid-turn with the countdown, and the scheduled jobs that won't run.
func TestQuitDialogSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{CrashDir: t.TempDir()})
	shell.Draining(s.Headless, s.Now.Add(42*time.Second), []core.SessionState{{ID: "rounding", Name: "Round half-even"}},
		[]host.Job{{Name: "sweep", Title: "Sweep stale deps", Schedule: host.Schedule{Every: "weekdays", At: "07:00"}}})
	for range 3 {
		s.Frame()
	}
	s.Lint("the quit dialog")
	s.Golden("quit")
	if text := s.Text(); !apptest.HasAll(text, "QUITTING ORIEL", "Round half-even", "in 42s", "Sweep stale deps", "Cancel", "Quit now") {
		t.Errorf("the dialog reads:\n%s", text)
	}
}

// Quitting with agents running, none of them mid-turn, still shows what's happening while they stop: the window says
// it's shutting down and lists the agents still stopping, rather than looking frozen. It takes no other keys.
func TestQuitShowsTheShutdown(t *testing.T) {
	r, lw, _ := quitScene(t, nil)
	lw.Fake.HoldClose = make(chan struct{})
	release := sync.OnceFunc(func() { close(lw.Fake.HoldClose) })
	t.Cleanup(release) // a failure before the release mustn't leave the host's Close waiting
	r.Win.RequestClose()
	r.WaitFrame("the shutdown", func(s string) bool {
		return apptest.HasAll(s, "SHUTTING DOWN", "Stopping 1 agent", "rounding")
	})
	r.TypeKeys("<D-k>")
	time.Sleep(150 * time.Millisecond)
	f := r.Latest("the shutdown", func(f headless.Frame) bool { return strings.Contains(f.Text(), "SHUTTING DOWN") })
	if strings.Contains(f.Text(), "Search sessions, commands") {
		t.Error("the omnibox opened over the shutdown")
	}
	release()
	r.WaitExit()
	if !handedOver(lw) {
		t.Error("the agent wasn't handed over")
	}
}
