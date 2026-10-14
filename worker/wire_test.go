package worker

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/wire"
)

// app is an app connected to the rig's worker over the wire, mirroring its state.
func (r *rig) app() (Client, *eventstore.Store) {
	r.t.Helper()
	a, b := wire.Pipe()
	r.w.Serve(b)
	r.t.Cleanup(func() { a.Close(); b.Close() })
	c := Client{Conn: a}
	store := eventstore.New()
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	go c.Events(ctx, store.Apply)
	return c, store
}

func waitStore(t *testing.T, s *eventstore.Store, what string, cond func(core.State) bool) core.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := s.State()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; app state is %+v", what, st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAppMirrorsTheWorkerOverTheWire(t *testing.T) {
	r := newRig(t)
	before := r.start(core.SessionSpec{Kind: core.KindScratch, Name: "notes"})
	c, store := r.app()
	waitStore(t, store, "the session there before connecting", func(st core.State) bool {
		_, ok := st.Session(before)
		return ok && len(st.Workers) == 1 && st.Workers[0].Connected
	})
	id, err := c.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "remote", Prompt: "hi", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err != nil {
		t.Fatal(err)
	}
	st := waitStore(t, store, "the new session and its first request", func(st core.State) bool {
		se, ok := st.Session(id)
		return ok && len(se.Threads()) == 1
	})
	se, _ := st.Session(id)
	if got := se.Snapshot(); got.Worker != core.LocalWorker || len(got.Projects) != 1 {
		t.Errorf("mirrored = %+v", got)
	}
	if hits, err := c.Search(context.Background(), SearchReq{Query: "hi"}); err != nil || len(hits) != 1 {
		t.Errorf("search over the wire = %+v, %v", hits, err)
	}
}

func TestSessionLogsLiveOnTheWorkerAndSurviveARestart(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindScratch})
	c, _ := r.app()
	router := &logs.Router{Sessions: c.Logs()}
	router.Write(logs.Record{Time: r.now, Level: slog.LevelWarn, Source: "lsp:gopls", Session: id, Msg: "gopls restarted"})
	if err := router.Err(); err != nil {
		t.Fatal(err)
	}
	r.restart()
	c, _ = r.app()
	recs, err := c.Logs().Open(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recs.Records()
	if err != nil || len(got) != 1 || got[0].Msg != "gopls restarted" {
		t.Errorf("records after a worker restart = %+v, %v", got, err)
	}
}

func TestTheCLIReachesTheWorkerOnItsSocket(t *testing.T) {
	r := newRig(t)
	dir, err := os.MkdirTemp("", "ow")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := SocketPath(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.w.Listen(ctx, path)
	var c Client
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err = DialLocal(ctx, dir, "/nonexistent"); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Conn.Close()
	info, err := c.Info(ctx)
	if err != nil || info.ID != core.LocalWorker {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, %v; want only its user", fi.Mode(), err)
	}
	if err := r.w.Listen(ctx, path); err == nil {
		t.Error("a second worker listened on the same socket")
	}
}

// AI-4: the launcher's modes are the worker's kinds, built-ins in sessions.md's order and a plugin's after them,
// each saying what it starts from.
func TestKindsReachTheLauncher(t *testing.T) {
	r := newRig(t)
	off := r.w.Kinds().Register(KindSpec{Kind: "incident", Title: "Incident", Pack: "work", Worktrees: WorktreesNow})
	defer off()
	c, _ := r.app()
	kinds, err := c.Kinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range kinds {
		got = append(got, string(k.Kind)+":"+string(k.From))
	}
	want := []string{"plan:prompt", "work:prompt", "review:pr", "tour:prompt", "job:prompt", "newrepo:name", "direct:dir", "scratch:nothing", "incident:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

// listenOn serves w on a fresh socket directory and waits until it answers.
func listenOn(t *testing.T, ctx context.Context, w *Worker) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ow")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	go w.Listen(ctx, SocketPath(dir))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := dial(SocketPath(dir)); err == nil {
			c.Conn.Close()
			return dir
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker never listened")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sessions.md → As built: an app reaching a worker from another build hands its sessions over to one of its own,
// which resumes the agents the old one had running.
func TestTheAppReplacesAWorkerFromAnotherBuild(t *testing.T) {
	r := newRig(t)
	r.w.opts.Build = "old"
	id, _ := r.workSession("start")
	r.eventually(id, "running", func(se core.SessionState) bool { return se.Agent.Running })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	old := r.w
	dir := listenOn(t, ctx, old)
	c, err := dialLocal(ctx, dir, Build(), func() error {
		r.open()
		go r.w.Listen(ctx, SocketPath(dir))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Conn.Close()
	if info, err := c.Info(ctx); err != nil || info.Build != Build() {
		t.Fatalf("info = %+v, %v; want this build's worker", info, err)
	}
	select {
	case <-old.closed:
	default:
		t.Error("the old worker is still running")
	}
	r.agent()
	if se := r.eventually(id, "resumed", func(se core.SessionState) bool { return se.Agent.Running }); len(r.fake.Resumed()) != 1 {
		t.Errorf("resumed %v; agent %+v", r.fake.Resumed(), se.Agent)
	}
}

// A worker from another build with an agent mid-turn stays: the app waits for the turn (app/workerrestart.go).
func TestDialKeepsAnotherBuildsWorkerMidTurn(t *testing.T) {
	r := newRig(t)
	r.w.opts.Build = "old"
	id, a := r.workSession("start")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := listenOn(t, ctx, r.w)
	c, err := dialLocal(ctx, dir, "new", func() error {
		t.Error("started another worker")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Conn.Close()
	if info, err := c.Info(ctx); err != nil || info.Build != "old" {
		t.Fatalf("info = %+v, %v; want the worker mid-turn", info, err)
	}
}
