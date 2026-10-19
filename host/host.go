// Package host is the session host: workspaces and worktrees, agents, PTYs (architecture.md). It runs in the
// app's process. It is the source of truth for session state: every change is a core event, applied to the store
// it shares with the app, and kept in a log per session so it survives a restart.
package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/testrun"
)

// Options configure a host.
type Options struct {
	Home      string // ~/.oriel: sessions/, workspaces/, repos.json
	Providers *agent.Providers
	Exec      agent.Exec // how agents' processes start; required to start one
	OrielBin  string     // the oriel binary the agents' hooks run; os.Executable() if ""
	Log       *slog.Logger
	Now       func() time.Time
	Load      func() sysload.Reading // the machine's load; sysload.Read if nil
	Version   string
	// Sandbox makes the container, gateway and egress proxy for a session that asks for one; nil refuses.
	Sandbox SandboxFactory
	// Packs finds a kind's prompt pack (agent/packs.Get, with plugins' and the user's overrides).
	Packs func(name string) (agent.Pack, bool)
	// ReposDir is where repos.clone and new repos go; ~/src if "".
	ReposDir string
	// GitHub is the GitHub client for a checkout, with the git guard's env; the user's gh (gh.CLI) if nil.
	GitHub func(dir string, env []string) gh.Client
	// KeyWait is how long a git operation that may need a security-key touch runs before its session asks for one;
	// KeyWait (the constant) if 0, never if negative.
	KeyWait time.Duration
}

// Host hosts sessions.
type Host struct {
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

	utility utilities

	shipOnce sync.Once
	mirror   mirrorState // GitHub review mirroring
	ship     *shipState

	mu       sync.Mutex
	recs     map[core.SessionID]*sessionLog
	live     map[core.SessionID]*session // the sessions with an agent running or starting
	app      App
	repos    *repoList
	sessLogs logs.Dirs
	open     map[core.SessionID]logs.SessionLog
	loginMu  sync.Mutex
	lost     map[string]*lostLogin // logins gone, by provider and account
	// sandboxCfg is the sandbox settings the app last sent.
	sandboxCfg sandbox.Config
	// prompts is the user's versions of the prompts the app last sent; promptsSent, whether it has.
	prompts     prompts.Overrides
	promptsSent bool
	closed      chan struct{}
	starts      sync.WaitGroup // agents starting off their callers (host.startAside)
	stopOnce    sync.Once
	closeErr    error
}

type sessionLog struct {
	f   *os.File
	rec *core.Recorder
}

// New starts a host: it reads back every session it had.
func New(opts Options) (*Host, error) {
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
	if opts.Load == nil {
		opts.Load = sysload.Read
	}
	if opts.OrielBin == "" {
		opts.OrielBin, _ = os.Executable()
	}
	if opts.ReposDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.ReposDir = filepath.Join(home, "src")
		}
	}
	if err := os.MkdirAll(filepath.Join(opts.Home, "sessions"), 0o700); err != nil {
		return nil, err
	}
	w := &Host{opts: opts, store: eventstore.New(), log: opts.Log, kinds: builtinKinds(),
		recs: map[core.SessionID]*sessionLog{}, live: map[core.SessionID]*session{}, open: map[core.SessionID]logs.SessionLog{},
		repos: &repoList{path: filepath.Join(opts.Home, "repos.json"), now: opts.Now, scanned: map[string]bool{}}, sessLogs: logs.Dirs{Root: filepath.Join(opts.Home, "sessions")},
		closed: make(chan struct{})}
	if err := w.repos.load(); err != nil {
		return nil, err
	}
	if err := w.loadSandboxConfig(); err != nil {
		return nil, err
	}
	w.loadPrompts()
	if err := w.restore(); err != nil {
		return nil, err
	}
	w.restoreUsage()
	w.store.Subscribe(core.Filter{}, w.record)
	w.settle()
	_ = w.emit(core.MachineChanged{Machine: w.machineState()})
	w.reposChanged()
	w.resumeHandedOver()
	return w, nil
}

// HandOver stops every agent for the next start to resume, then closes the host: Oriel quitting.
func (w *Host) HandOver() {
	defer w.recover("HandOver", nil)
	w.mu.Lock()
	live := make([]*session, 0, len(w.live))
	for _, h := range w.live {
		live = append(live, h)
	}
	w.mu.Unlock()
	for _, h := range live {
		h.stop("Oriel quit", core.StoppedByHandOver)
	}
	w.Close()
}

// resumeHandedOver starts again the agents the last process handed over, or had running when it crashed.
func (w *Host) resumeHandedOver() {
	for _, se := range w.store.State().Sessions {
		if by := se.Agent.StoppedBy; !se.Agent.Running && (by == core.StoppedByHandOver || by == core.StoppedByCrash) {
			w.goSafe("resume", func() {
				if err := w.Resume(context.Background(), se.ID); err != nil {
					w.log.Warn("resume after the last run", "session", se.ID, "by", by, "err", err)
				}
			})
		}
	}
}

// Store is the session host's state, which the app shares.
func (w *Host) Store() core.Store { return w.store }

// Kinds is the session kinds the host starts: built-ins, and any a plugin adds.
func (w *Host) Kinds() *Kinds { return w.kinds }

func (w *Host) sessionDir(id core.SessionID) string {
	return filepath.Join(w.opts.Home, "sessions", string(id))
}

// restore replays each session's log, dropping crit's workspaces. A log that stops early (a torn last line) keeps
// what it had.
func (w *Host) restore() error {
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

// settle puts back what the last process left running when it ended without handing over: those agents stopped
// with it (StoppedByCrash), and nobody is asked anything.
func (w *Host) settle() {
	for _, se := range w.store.State().Sessions {
		if se.Agent.Running || se.Agent.Working || se.Agent.Starting != nil || len(se.Asks) > 0 {
			a := se.Agent
			a.Stopped(core.StoppedByCrash, w.opts.Now())
			_ = w.store.Apply(core.AgentChanged{ID: se.ID, Agent: a})
			for _, ask := range se.Asks {
				_ = w.store.Apply(core.AskClosed{Session: se.ID, ID: ask.ID, Decision: "gone"})
			}
			w.restatus(se.ID)
		}
		for _, rv := range se.AIReviews {
			if rv.Running {
				rv.Running, rv.Ended, rv.Error = false, w.opts.Now().UTC(), "Oriel ended during the review"
				_ = w.store.Apply(core.AIReviewChanged{Session: se.ID, Review: rv})
			}
		}
	}
}

// compact rewrites a session's log as the one event that makes it as it is now.
func (w *Host) compact(id core.SessionID) error {
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

func (w *Host) openLog(id core.SessionID, seq uint64) error {
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
func (w *Host) record(ev core.Event) {
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

// emit applies an event to the host's state.
func (w *Host) emit(ev core.Event) error {
	if err := w.store.Apply(ev); err != nil {
		w.log.Warn("event rejected", "type", core.EventName(ev), "err", err)
		return err
	}
	return nil
}

// session is a copy of a session's state.
func (w *Host) session(id core.SessionID) (core.SessionState, error) {
	se, ok := w.store.State().Session(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %s", id)
	}
	return se.Snapshot(), nil
}

// restatus sets a session's status from what's in it.
func (w *Host) restatus(id core.SessionID) {
	se, err := w.session(id)
	if err != nil {
		return
	}
	if s := core.DeriveStatus(se); s != se.Status {
		_ = w.emit(core.SessionStatusChanged{ID: id, Status: s})
	}
}

// Close stops every agent, without handing them over, and closes the logs. Only the first call does anything.
func (w *Host) Close() error {
	w.stopOnce.Do(func() {
		defer w.recover("Close", &w.closeErr)
		close(w.closed)
		w.mu.Lock()
		live := make([]*session, 0, len(w.live))
		for _, h := range w.live {
			live = append(live, h)
		}
		w.mu.Unlock()
		for _, h := range live {
			h.stop("the session host closed", "")
		}
		w.starts.Wait()
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, l := range w.recs {
			w.closeErr = errors.Join(w.closeErr, l.f.Close())
		}
		for _, l := range w.open {
			w.closeErr = errors.Join(w.closeErr, l.Close())
		}
	})
	return w.closeErr
}

// Search finds text in sessions' threads and transcripts: archived ones keep theirs, so they stay searchable.
func (w *Host) Search(q SearchReq) (out []Hit) {
	defer w.recover("Search", nil)
	needle := strings.ToLower(strings.TrimSpace(q.Query))
	if needle == "" {
		return nil
	}
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
func (w *Host) transcript(id core.SessionID) []core.Item {
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
