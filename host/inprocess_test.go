package host

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host/sysload"
)

// The app's store is the host's: what the host does is in it at once, with this machine's state.
func TestTheAppSharesTheHostsStore(t *testing.T) {
	r := newRig(t)
	id, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "shared", Prompt: "hi", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err != nil {
		t.Fatal(err)
	}
	st := r.w.Store().State()
	se, ok := st.Session(id)
	if !ok || len(se.Snapshot().Projects) != 1 || len(se.Threads()) != 1 {
		t.Fatalf("the store has %+v", st.Sessions)
	}
	if st.Machine.OS == "" || st.Machine.Sampled.IsZero() {
		t.Errorf("machine = %+v", st.Machine)
	}
	if hits := r.w.Search(SearchReq{Query: "hi"}); len(hits) != 1 {
		t.Errorf("search = %+v", hits)
	}
}

func TestSessionLogsSurviveARestart(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan})
	router := &logs.Router{Sessions: r.w.Logs()}
	router.Write(logs.Record{Time: r.now(), Level: slog.LevelWarn, Source: "lsp:gopls", Session: id, Msg: "gopls restarted"})
	if err := router.Err(); err != nil {
		t.Fatal(err)
	}
	r.restart()
	recs, err := r.w.Logs().Open(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recs.Records()
	if err != nil || len(got) != 1 || got[0].Msg != "gopls restarted" {
		t.Errorf("records after a restart = %+v, %v", got, err)
	}
}

// AI-4: the host's kinds, built-ins in sessions.md's order and a plugin's after them, each saying what it starts
// from; the launcher offers those marked Launcher: Work, Plan and Review PR, and a plugin's that asks to be there.
func TestKindsReachTheLauncher(t *testing.T) {
	r := newRig(t)
	off := r.w.Kinds().Register(KindSpec{Kind: "incident", Title: "Incident", Pack: "work", Worktrees: WorktreesNow, Launcher: true})
	defer off()
	var got, launcher []string
	for _, k := range r.w.Kinds().All() {
		got = append(got, string(k.Kind)+":"+string(k.From))
		if k.Launcher {
			launcher = append(launcher, string(k.Kind))
		}
	}
	want := []string{"work:prompt", "plan:prompt", "review:pr", "job:prompt", "newrepo:name", "direct:dir", "incident:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("kinds = %v, want %v", got, want)
	}
	if want := []string{"work", "plan", "review", "incident"}; strings.Join(launcher, " ") != strings.Join(want, " ") {
		t.Errorf("launcher kinds = %v, want %v", launcher, want)
	}
}

// Quitting hands the agents over: the next start resumes them.
func TestTheNextStartResumesAHandOver(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Running && se.Agent.Session == a.ID() })
	r.w.HandOver()
	if se := r.session(id); se.Agent.Running || se.Agent.StoppedBy != core.StoppedByHandOver {
		t.Fatalf("handed over: %+v", se.Agent)
	}
	r.open()
	r.agent()
	if se := r.eventually(id, "resumed", func(se core.SessionState) bool { return se.Agent.Running }); len(r.fake.Resumed()) != 1 {
		t.Errorf("resumed %v; agent %+v", r.fake.Resumed(), se.Agent)
	}
}

// PLAN.md, "A crash resumes on the next launch": agents a crash took with it are marked StoppedByCrash and resumed.
func TestTheNextStartResumesWhatACrashStopped(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Running && se.Agent.Session == a.ID() })
	r.open() // the last process never closed
	r.agent()
	se := r.eventually(id, "resumed", func(se core.SessionState) bool { return se.Agent.Running })
	if len(r.fake.Resumed()) != 1 {
		t.Errorf("resumed %v; agent %+v", r.fake.Resumed(), se.Agent)
	}
}

// PLAN.md, "Not crashing": a panic in a host method, a goroutine of the host's or a callback into the app is an
// error, logged; the process goes on.
func TestAPanicInTheHostIsLoggedNotFatal(t *testing.T) {
	r := newRig(t)
	var mu sync.Mutex
	var buf bytes.Buffer
	r.w.log = slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, nil))
	logged := func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}

	now := r.w.opts.Now
	r.w.opts.Now = func() time.Time { panic("the clock broke") }
	_, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindPlan})
	r.w.opts.Now = now
	if err == nil || !strings.Contains(err.Error(), "the clock broke") {
		t.Errorf("Start = %v, want the panic as an error", err)
	}

	r.w.opts.Load = func() sysload.Reading { panic("no load") }
	ctx, cancel := context.WithCancel(context.Background())
	go r.w.SampleLoad(ctx, time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(logged(), "no load"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("SampleLoad's panic wasn't logged")
		}
	}
	cancel()

	r.w.SetApp(App{Decide: func(context.Context, Decide) Verdict { panic("a hook broke") }})
	id, a := r.workSession("Work")
	ans := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Summary: "ls"})
	r.eventually(id, "the card, as no hook answered", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	r.w.Answer(AnswerReq{Session: id, Ask: "p1", Decision: "allow"})
	if d := <-ans; !d.Allow {
		t.Errorf("answer = %+v", d)
	}

	for _, want := range []string{"the clock broke", "no load", "a hook broke"} {
		if !strings.Contains(logged(), want) {
			t.Errorf("the log has no %q:\n%s", want, logged())
		}
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Quitting stops every agent at once, not one after another: each stop takes its sandbox down, so N sessions would
// take N times as long.
func TestHandOverStopsTheAgentsTogether(t *testing.T) {
	r := newRig(t)
	ids := []core.SessionID{}
	for _, name := range []string{"one", "two"} {
		id, a := r.workSession(name)
		r.eventually(id, "the agent", func(se core.SessionState) bool { return se.Agent.Running && se.Agent.Session == a.ID() })
		ids = append(ids, id)
	}
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	defer release()
	r.fake.HoldClose, r.fake.Closing = hold, make(chan *agenttest.FakeSession, 4)
	done := make(chan struct{})
	go func() { r.w.HandOver(); close(done) }()
	for range ids {
		select {
		case <-r.fake.Closing:
		case <-time.After(5 * time.Second):
			t.Fatal("the agents stop one after another")
		}
	}
	release()
	<-done
	for _, id := range ids {
		if se := r.session(id); se.Agent.Running || se.Agent.StoppedBy != core.StoppedByHandOver {
			t.Errorf("%s handed over: %+v", id, se.Agent)
		}
	}
}
