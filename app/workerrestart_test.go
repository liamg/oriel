package app

import (
	"context"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// oldBuildWorker is this machine's worker as another build left it: a session whose agent is mid-turn. The app's
// LocalWorker starts a worker of this build on the same home, which resumes the agent.
func oldBuildWorker(t *testing.T) (r *running, lw *localWorker, turn *agenttest.FakeSession) {
	t.Helper()
	lw = newWorkerWith(t, worker.Options{Build: "old"})
	id, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "rounding", Prompt: "Round half-even",
		Provider: "fake", Repos: []core.RepoID{core.RepoID(lw.repo)}})
	if err != nil {
		t.Fatal(err)
	}
	turn = lw.fake.WaitStarted(t)
	turn.WaitSent(t)
	turn.Emit(agent.TurnStart{})
	home := lw.w.Info().StateDir
	r = startAppWith(t, t.TempDir(), "", nil, func(cfg *Config) {
		lw.connect(t)(cfg)
		cfg.LocalWorker = func(context.Context) (worker.Client, error) {
			providers := &agent.Providers{}
			providers.Register(lw.fake)
			w, err := worker.New(worker.Options{Home: home, Providers: providers, Exec: nopExec{}, Load: steadyLoad,
				Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }})
			if err != nil {
				return worker.Client{}, err
			}
			t.Cleanup(func() { w.Close() })
			a, b := wire.Pipe()
			w.Serve(b)
			t.Cleanup(func() { a.Close(); b.Close() })
			return worker.Client{Conn: a}, nil
		}
	})
	waitState(t, r.store, "the turn", func(st core.State) bool {
		se, ok := st.Session(id)
		return ok && se.Snapshot().Agent.Working
	})
	return r, lw, turn
}

// restarted waits for the worker of this build: the dialog gone, the session there, its agent resumed.
func restarted(t *testing.T, r *running, lw *localWorker) {
	t.Helper()
	r.waitGone("RESTARTING THE WORKER")
	lw.fake.WaitStarted(t)
	waitState(t, r.store, "the agent back", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Snapshot().Agent.Running && !se.Snapshot().Agent.Working
	})
	if got := lw.fake.Resumed(); len(got) != 1 {
		t.Errorf("resumed %v; want the session's agent once", got)
	}
}

// sessions.md → As built: a worker from another build with an agent mid-turn hands over when the turn ends; a
// dialog says what it waits for meanwhile, and hiding it doesn't stop the restart.
func TestWorkerRestartWaitsForTheTurn(t *testing.T) {
	r, lw, turn := oldBuildWorker(t)
	r.waitFrame("the restart dialog", func(s string) bool {
		return containsAll(s, "RESTARTING THE WORKER", "rounding", "mid-turn", "Restart now")
	})
	r.typeKeys("<Esc>")
	r.waitGone("RESTARTING THE WORKER")
	if got := lw.fake.Resumed(); len(got) != 0 {
		t.Fatalf("restarted mid-turn: resumed %v", got)
	}
	turn.Emit(agent.TurnEnd{})
	restarted(t, r, lw)
}

// Restart now doesn't wait for the turn.
func TestWorkerRestartNow(t *testing.T) {
	r, lw, _ := oldBuildWorker(t)
	r.waitText("RESTARTING THE WORKER")
	r.clickTop("Restart now")
	restarted(t, r, lw)
}

// A turn still going when the wait is up is cut short.
func TestWorkerRestartStopsWaiting(t *testing.T) {
	was := workerDrainWait
	workerDrainWait = time.Second
	t.Cleanup(func() { workerDrainWait = was })
	r, lw, _ := oldBuildWorker(t)
	restarted(t, r, lw)
}

// The dialog while it waits: the sessions mid-turn, and the countdown.
func TestWorkerRestartDialogSnapshot(t *testing.T) {
	s := newErrScene(t, t.TempDir())
	s.sh.draining.Store(&workerDrain{restart: true, worker: core.LocalWorker, deadline: s.now.Add(42 * time.Second), focus: true,
		busy: []core.SessionState{{ID: "eng-2231-fix-stale-usage-prices"}, {ID: "rounding", Name: "Round half-even"}}})
	for range 3 {
		s.frame()
	}
	s.lint("the worker restart dialog")
	s.golden("worker-restart")
	if text := s.frameText(); !containsAll(text, "RESTARTING THE WORKER", "eng-2231-fix-stale-usage-prices", "Round half-even", "in 42s",
		"Hide", "Restart now") {
		t.Errorf("the dialog reads:\n%s", text)
	}
}

// Quitting while a restart waits turns it into the quit; Cancel takes it back to the restart.
func TestQuitDuringAWorkerRestart(t *testing.T) {
	r, lw, turn := oldBuildWorker(t)
	r.waitText("RESTARTING THE WORKER")
	r.win.RequestClose()
	r.waitText("QUITTING ORIEL")
	r.typeKeys("<Esc>")
	r.waitText("RESTARTING THE WORKER")
	if r.win.Closed() {
		t.Fatal("Cancel quit anyway")
	}
	turn.Emit(agent.TurnEnd{})
	restarted(t, r, lw)
}
