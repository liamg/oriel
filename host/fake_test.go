package host

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/isolate"
)

type nopExec struct{ agent.Exec }

// rig is a host on a temporary home, with a fake provider and a fixture repo.
type rig struct {
	t     *testing.T
	w     *Host
	home  string
	fake  *agenttest.Fake
	repo  *gittest.Repo
	clock *rigClock        // the host's clock: read through now, set through setNow
	more  []agent.Provider // beside the fake
	tools []string         // the packs' tools
	log   *slog.Logger     // the host's log; none if nil
	bare  bool             // open the host with no providers of its own: the app's come later (SetProviders)
}

func newRig(t *testing.T, more ...agent.Provider) *rig {
	t.Helper()
	isolate.Test(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")
	t.Setenv("USER", "tester")
	r := &rig{t: t, home: t.TempDir(), fake: agenttest.NewFake(), clock: &rigClock{Fake: clocktest.New(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))},
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
	if !r.bare {
		providers.Register(r.fake)
		for _, p := range r.more {
			providers.Register(p)
		}
	}
	w, err := New(Options{Home: r.home, Providers: providers, Exec: nopExec{}, Clock: r.clock, Log: r.log, HTTP: isolate.Net(r.t),
		Packs: func(name string) (agent.Pack, bool) {
			return agent.Pack{Name: name, Skill: name, Text: "pack " + name, Tools: r.tools}, true
		}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.w = w
	r.t.Cleanup(func() { w.Close() })
}

func (r *rig) now() time.Time { return r.clock.Fake.Now() }

func (r *rig) setNow(t time.Time) { r.clock.Set(t) }

// rigClock is a clock a test moves, or breaks: while broke is set, Now panics with it.
type rigClock struct {
	*clocktest.Fake
	mu    sync.Mutex
	broke string
}

func (c *rigClock) Now() time.Time {
	c.mu.Lock()
	broke := c.broke
	c.mu.Unlock()
	if broke != "" {
		panic(broke)
	}
	return c.Fake.Now()
}

// breakWith has Now panic with why; "" mends it.
func (c *rigClock) breakWith(why string) {
	c.mu.Lock()
	c.broke = why
	c.mu.Unlock()
}

// restart closes the host and starts another on the same home.
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

// agent is the fake session the host started.
func (r *rig) agent() *agenttest.FakeSession {
	r.t.Helper()
	return r.fake.WaitStarted(r.t)
}

// waitTimeout is how long a wait on the host's work goes before it's taken as hung.
const waitTimeout = 30 * time.Second

// until waits for cond, failing with what (and more, if given) once the host looks hung.
func until(t testing.TB, what string, cond func() bool, more ...func() string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			detail := ""
			for _, m := range more {
				detail += "; " + m()
			}
			t.Fatalf("waiting for %s%s", what, detail)
		}
		time.Sleep(time.Millisecond) // a poll: the condition is the host's work, not a while
	}
}

// eventually waits for cond on the session's state.
func (r *rig) eventually(id core.SessionID, what string, cond func(core.SessionState) bool) core.SessionState {
	r.t.Helper()
	var se core.SessionState
	until(r.t, what, func() bool { se = r.session(id); return cond(se) }, func() string { return fmt.Sprintf("session is %+v", se) })
	return se
}

// settle waits for the host's work off its callers to end: agents starting, resumes, marks, notices.
func (r *rig) settle() {
	r.t.Helper()
	until(r.t, "the host's work to end", func() bool { return len(r.w.Busy()) == 0 }, func() string { return fmt.Sprintf("still running %q", r.w.Busy()) })
}

// noneSent checks a was given nothing: once its events are handled and the host's work has ended, nothing is on its way.
func (r *rig) noneSent(a *agenttest.FakeSession) {
	r.t.Helper()
	a.Handled(r.t)
	r.settle()
	select {
	case in := <-a.Sent:
		r.t.Fatalf("the agent was sent %q, want nothing yet", in.Text)
	default:
	}
}

// noStart fails if an agent started: once the host's work has ended, none is on its way.
func (r *rig) noStart(what string) {
	r.t.Helper()
	r.settle()
	select {
	case <-r.fake.Started:
		r.t.Fatalf("an agent started: %s", what)
	default:
	}
}

// withKind gives the host a kind beside the built-ins, as a plugin would, until the func it returns.
func (r *rig) withKind(k core.KindSpec) func() {
	r.w.SetKinds(plusKind{k})
	return func() { r.w.SetKinds(nil) }
}

type plusKind struct{ core.KindSpec }

func (p plusKind) Kind(k core.Kind) (core.KindSpec, bool) {
	if k == p.KindSpec.Kind {
		return p.KindSpec, true
	}
	return builtins{}.Kind(k)
}
