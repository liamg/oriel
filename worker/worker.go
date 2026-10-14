// Package worker is the per-machine session host: workspaces and worktrees, agents, PTYs (architecture.md).
// It is the source of truth for session state: every change is a core event, applied to its own store, kept in a
// log per session so it survives a restart, and streamed to the app.
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker/stats"
)

// Options configure a worker.
type Options struct {
	ID        core.WorkerID // LocalWorker if ""
	Name      string        // the machine's name if ""
	Home      string        // ~/.oriel: sessions/, workspaces/, repos.json
	Providers *agent.Providers
	Exec      agent.Exec // how agents' processes start; required to start one
	OrielBin  string     // the oriel binary the agents' hooks run; os.Executable() if ""
	Log       *slog.Logger
	Now       func() time.Time
	Version   string
	Build     string // which binary this is, so an app from another can replace it; Build() if ""
	// Sandbox makes the container, gateway and egress proxy for a session that asks for one; nil refuses.
	Sandbox SandboxFactory
	// Packs finds a kind's prompt pack (agent/packs.Get, with plugins' and the user's overrides).
	Packs func(name string) (agent.Pack, bool)
	// ReposDir is where repos.clone and new repos go; ~/src if "".
	ReposDir string
	// TrustPath is the trusted.toml project settings files are trusted in, the app's on this machine; Home's if "".
	TrustPath string
}

// Worker hosts sessions.
type Worker struct {
	opts  Options
	store *eventstore.Store
	log   *slog.Logger
	kinds *Kinds

	edit      sync.Mutex // one read-modify-write of a session's state at a time
	testsOnce sync.Once
	testSet   *testrun.Set

	statsOnce sync.Once
	statsData *stats.Store
	statsMu   sync.Mutex // the stats store, and the events records it reads
	usageMu   sync.Mutex

	jobsMu sync.Mutex // the jobs' files, and when the schedules last ran

	mu       sync.Mutex
	recs     map[core.SessionID]*sessionLog
	hosts    map[core.SessionID]*host
	apps     []wire.Conn
	repos    *repoList
	sessLogs logs.Dirs
	open     map[core.SessionID]logs.SessionLog
	loginMu  sync.Mutex
	lost     map[string]*lostLogin // logins gone, by provider and account
	// sandboxCfg is the sandbox settings the app last sent.
	sandboxCfg sandbox.Config
	closed     chan struct{}
	stopOnce   sync.Once
}

type sessionLog struct {
	f   *os.File
	rec *core.Recorder
}

// New starts a worker: it reads back every session it had.
func New(opts Options) (*Worker, error) {
	if opts.ID == "" {
		opts.ID = core.LocalWorker
	}
	if opts.Name == "" {
		opts.Name, _ = os.Hostname()
	}
	if opts.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		opts.Home = filepath.Join(home, ".oriel")
	}
	if opts.Providers == nil {
		opts.Providers = &agent.Providers{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Build == "" {
		opts.Build = Build()
	}
	if opts.OrielBin == "" {
		opts.OrielBin, _ = os.Executable()
	}
	if opts.ReposDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.ReposDir = filepath.Join(home, "src")
		}
	}
	if opts.TrustPath == "" {
		opts.TrustPath = filepath.Join(opts.Home, "trusted.toml")
	}
	if err := os.MkdirAll(filepath.Join(opts.Home, "sessions"), 0o700); err != nil {
		return nil, err
	}
	w := &Worker{opts: opts, store: eventstore.New(), log: opts.Log, kinds: builtinKinds(),
		recs: map[core.SessionID]*sessionLog{}, hosts: map[core.SessionID]*host{}, open: map[core.SessionID]logs.SessionLog{},
		repos: &repoList{path: filepath.Join(opts.Home, "repos.json")}, sessLogs: logs.Dirs{Root: filepath.Join(opts.Home, "sessions")},
		closed: make(chan struct{})}
	if err := w.repos.load(); err != nil {
		return nil, err
	}
	if err := w.loadSandboxConfig(); err != nil {
		return nil, err
	}
	if err := w.restore(); err != nil {
		return nil, err
	}
	w.restoreUsage()
	w.store.Subscribe(core.Filter{}, w.record)
	w.settle()
	w.resumeHandedOver()
	return w, nil
}

// HandOver stops every agent for the next worker to resume, then the worker: one from another build takes over.
func (w *Worker) HandOver() {
	w.mu.Lock()
	hosts := make([]*host, 0, len(w.hosts))
	for _, h := range w.hosts {
		hosts = append(hosts, h)
	}
	w.mu.Unlock()
	for _, h := range hosts {
		h.stop("handed over to another worker", core.StoppedByHandOver)
	}
	w.Close()
}

// resumeHandedOver starts again the agents the last worker handed over.
func (w *Worker) resumeHandedOver() {
	for _, se := range w.store.State().Sessions {
		if !se.Agent.Running && se.Agent.StoppedBy == core.StoppedByHandOver {
			go func() {
				if err := w.Resume(context.Background(), se.ID); err != nil {
					w.log.Warn("resume after a hand-over", "session", se.ID, "err", err)
				}
			}()
		}
	}
}

// Store is the worker's state.
func (w *Worker) Store() core.Store { return w.store }

// Kinds is the session kinds this worker starts: built-ins, and any a plugin adds.
func (w *Worker) Kinds() *Kinds { return w.kinds }

func (w *Worker) sessionDir(id core.SessionID) string {
	return filepath.Join(w.opts.Home, "sessions", string(id))
}

// restore replays each session's log, dropping crit's workspaces. A log that stops early (a torn last line) keeps
// what it had.
func (w *Worker) restore() error {
	entries, err := os.ReadDir(filepath.Join(w.opts.Home, "sessions"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := core.SessionID(e.Name())
		path := filepath.Join(w.sessionDir(id), "events.jsonl")
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		n, err := core.ReplayCount(bufio.NewReader(f), w.store)
		f.Close()
		if se, ok := w.store.State().Session(id); ok && se.Snapshot().Source == "crit" {
			_ = w.store.Apply(core.SessionClosed{ID: id})
			_ = os.RemoveAll(w.sessionDir(id))
			continue
		}
		if err != nil {
			w.log.Warn("session log stops early", "session", id, "after", n, "err", err)
			if err := w.compact(id); err != nil {
				return err
			}
			continue
		}
		if err := w.openLog(id, n); err != nil {
			return err
		}
	}
	return nil
}

// settle puts back what a restart stopped: nothing runs until it's resumed, and nobody is asked anything.
func (w *Worker) settle() {
	for _, se := range w.store.State().Sessions {
		if se.Agent.Running || se.Agent.Working || len(se.Asks) > 0 {
			a := se.Agent
			a.Stopped(core.StoppedByWorker, w.opts.Now())
			_ = w.store.Apply(core.AgentChanged{ID: se.ID, Agent: a})
			for _, ask := range se.Asks {
				_ = w.store.Apply(core.AskClosed{Session: se.ID, ID: ask.ID, Decision: "gone"})
			}
			w.restatus(se.ID)
		}
	}
}

// compact rewrites a session's log as the one event that makes it as it is now.
func (w *Worker) compact(id core.SessionID) error {
	se, ok := w.store.State().Session(id)
	path := filepath.Join(w.sessionDir(id), "events.jsonl")
	_ = os.Rename(path, path+".broken")
	if !ok {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	rec := core.NewRecorder(f)
	rec.Record(core.SessionSynced{Session: se.Snapshot()})
	w.recs[id] = &sessionLog{f: f, rec: rec}
	return rec.Err()
}

func (w *Worker) openLog(id core.SessionID, seq uint64) error {
	if err := os.MkdirAll(w.sessionDir(id), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.sessionDir(id), "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.recs[id] = &sessionLog{f: f, rec: core.ResumeRecorder(f, seq)}
	return nil
}

// record keeps every session event in its session's log.
func (w *Worker) record(ev core.Event) {
	id := ev.Scope()
	if id == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	l := w.recs[id]
	if l == nil {
		if err := w.openLog(id, 0); err != nil {
			w.log.Error("session log", "session", id, "err", err)
			return
		}
		l = w.recs[id]
	}
	l.rec.Record(ev)
}

// emit applies an event to the worker's state.
func (w *Worker) emit(ev core.Event) error {
	if err := w.store.Apply(ev); err != nil {
		w.log.Warn("event rejected", "type", core.EventName(ev), "err", err)
		return err
	}
	return nil
}

// session is a copy of a session's state.
func (w *Worker) session(id core.SessionID) (core.SessionState, error) {
	se, ok := w.store.State().Session(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %s", id)
	}
	return se.Snapshot(), nil
}

// restatus sets a session's status from what's in it.
func (w *Worker) restatus(id core.SessionID) {
	se, err := w.session(id)
	if err != nil {
		return
	}
	if s := core.DeriveStatus(se); s != se.Status {
		_ = w.emit(core.SessionStatusChanged{ID: id, Status: s})
	}
}

// Close stops every agent and closes the logs.
func (w *Worker) Close() error {
	w.stopOnce.Do(func() { close(w.closed) })
	w.mu.Lock()
	hosts := make([]*host, 0, len(w.hosts))
	for _, h := range w.hosts {
		hosts = append(hosts, h)
	}
	w.mu.Unlock()
	for _, h := range hosts {
		h.stop("the worker stopped", core.StoppedByWorker)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	for _, l := range w.recs {
		err = errors.Join(err, l.f.Close())
	}
	for _, l := range w.open {
		err = errors.Join(err, l.Close())
	}
	return err
}

// Info is what the worker says about itself.
func (w *Worker) Info() Info {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return Info{ID: w.opts.ID, Name: w.opts.Name, Host: host, OS: runtime.GOOS, Home: home, Version: w.opts.Version, Build: w.opts.Build, StateDir: w.opts.Home}
}

// Search finds text in sessions' threads and transcripts: archived ones keep theirs, so they stay searchable.
func (w *Worker) Search(q SearchReq) []Hit {
	needle := strings.ToLower(strings.TrimSpace(q.Query))
	if needle == "" {
		return nil
	}
	var out []Hit
	for _, se := range w.store.State().Sessions {
		if se.Archived && !q.Archived {
			continue
		}
		for _, t := range se.Threads {
			for _, c := range t.Comments {
				if strings.Contains(strings.ToLower(c.Body), needle) {
					out = append(out, Hit{Session: se.ID, Thread: t.ID, Text: snippet(c.Body, needle)})
				}
			}
		}
		for _, it := range w.transcript(se.ID) {
			if strings.Contains(strings.ToLower(it.Text), needle) {
				out = append(out, Hit{Session: se.ID, Item: it.ID, Text: snippet(it.Text, needle)})
			}
		}
	}
	return out
}

// transcript is a session's whole transcript from its log, which keeps more than its state does.
func (w *Worker) transcript(id core.SessionID) []core.Item {
	f, err := os.Open(filepath.Join(w.sessionDir(id), "events.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var items []core.Item
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"TranscriptAppended"`) {
			continue
		}
		var rec struct {
			Event core.TranscriptAppended `json:"event"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		for _, it := range rec.Event.Items {
			if i, ok := seen[it.ID]; ok && it.ID != "" {
				items[i] = it
				continue
			}
			seen[it.ID] = len(items)
			items = append(items, it)
		}
	}
	return items
}

func snippet(s, needle string) string {
	i := strings.Index(strings.ToLower(s), needle)
	if i < 0 {
		return s
	}
	from, to := max(0, i-40), min(len(s), i+len(needle)+40)
	return strings.TrimSpace(strings.ReplaceAll(s[from:to], "\n", " "))
}

// apps are the app connections this worker serves, newest last.
func (w *Worker) appConns() []wire.Conn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.apps)
}
