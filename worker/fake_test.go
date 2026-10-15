package worker

import (
	"context"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

type nopExec struct{ agent.Exec }

// rig is a worker on a temporary home, with a fake provider and a fixture repo.
type rig struct {
	t     *testing.T
	w     *Worker
	home  string
	fake  *agenttest.Fake
	repo  *gittest.Repo
	now   time.Time
	more  []agent.Provider // beside the fake
	tools []string         // the packs' tools
}

func newRig(t *testing.T, more ...agent.Provider) *rig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")
	t.Setenv("USER", "tester")
	r := &rig{t: t, home: t.TempDir(), fake: agenttest.NewFake(), now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC),
		more: more}
	r.repo = gittest.New(t)
	r.repo.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() {}\n"})
	r.repo.Commit("Ada", "first")
	r.open()
	return r
}

func (r *rig) open() {
	r.t.Helper()
	providers := &agent.Providers{}
	providers.Register(r.fake)
	for _, p := range r.more {
		providers.Register(p)
	}
	w, err := New(Options{Home: r.home, Providers: providers, Exec: nopExec{}, Now: func() time.Time { return r.now },
		Packs: func(name string) (agent.Pack, bool) {
			return agent.Pack{Name: name, Skill: name, Text: "pack " + name, Tools: r.tools}, true
		}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.w = w
	r.t.Cleanup(func() { w.Close() })
}

// restart closes the worker and starts another on the same home.
func (r *rig) restart() {
	r.t.Helper()
	if err := r.w.Close(); err != nil {
		r.t.Fatal(err)
	}
	r.open()
}

func (r *rig) session(id core.SessionID) core.SessionState {
	r.t.Helper()
	se, err := r.w.session(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return se
}

func (r *rig) start(spec core.SessionSpec) core.SessionID {
	r.t.Helper()
	id, err := r.w.Start(context.Background(), spec)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

// agent is the fake session the worker started.
func (r *rig) agent() *agenttest.FakeSession {
	r.t.Helper()
	return r.fake.WaitStarted(r.t)
}

// eventually waits for cond on the session's state.
func (r *rig) eventually(id core.SessionID, what string, cond func(core.SessionState) bool) core.SessionState {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		se := r.session(id)
		if cond(se) {
			return se
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("waiting for %s; session is %+v", what, se)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
