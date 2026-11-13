// Package host is the session host: workspaces and worktrees, agents, PTYs (docs/architecture.md). It runs in the
// app's process. It is the source of truth for session state: every change is a core event, applied to the store
// it shares with the app, and kept in a log per session so it survives a restart.
//
// This package is the host's facade: the API the app calls, each entry point recovering a panic into an error. The
// work is done by its parts, each in a package of its own that sees the host only as a hostcore.State: worktree
// (workspaces), agentrun (agents), sandboxhost (sandboxes), github (code hosts and the Ship page), repos, jobs,
// utility, aireview, persist (what's kept on disk) and pricing (the price book). The parts never call each other;
// what one needs of another it's given here, as a func.
//
// Lock order, outermost first: a session's agent's lock (agentrun), then the edit lock (State.Mutate), then the
// store's. Every other lock (the runner's, the logs', the sandbox settings', the ship and mirror states') is held
// only while its own fields are read or written, never while calling out.
package host

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/agentrun"
	"github.com/liamg/oriel/host/aireview"
	"github.com/liamg/oriel/host/github"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/host/jobs"
	"github.com/liamg/oriel/host/persist"
	"github.com/liamg/oriel/host/pricing"
	"github.com/liamg/oriel/host/repos"
	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/host/utility"
	"github.com/liamg/oriel/host/worktree"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sysload"
)

// Options configure a host.
type Options struct {
	Home      string // ~/.oriel: sessions/, workspaces/, repos.json
	Providers *agent.Providers
	Exec      agent.Exec // how agents' processes start; required to start one
	OrielBin  string     // the oriel binary the agents' hooks run; os.Executable() if ""
	Log       *slog.Logger
	Clock     clock.Clock            // the system's if nil
	Load      func() sysload.Reading // the machine's load; a sysload.Reader of its own if nil
	Version   string
	// Sandbox makes the container, gateway and egress proxy for a session that asks for one; nil refuses.
	Sandbox SandboxFactory
	// Packs finds a kind's prompt pack (agent/packs.Get, with plugins' and the user's overrides).
	Packs func(name string) (agent.Pack, bool)
	// ReposDir is where repos.clone and new repos go; ~/src if "".
	ReposDir string
	// GitHub is the GitHub client for a checkout, with the git guard's env; GitHub's API with the user's token
	// (gh.TokenFor) if nil.
	GitHub func(dir string, env []string) gh.Client
	// KeyWait is how long a git operation that may need a security-key touch runs before its session asks for one;
	// KeyWait (the constant) if 0, never if negative.
	KeyWait time.Duration
	// HTTP is the client the host's own requests go through (cost.dev's prices, GitHub's API); http.DefaultClient if
	// nil.
	HTTP *http.Client
	// PricesURL is where the price list is fetched from; cost.dev's (price.URL) if "".
	PricesURL string
	// CloseWait is how long Close waits for what the host still has running (closeWait if 0): quitting never hangs
	// on one, and what's left is logged by name.
	CloseWait time.Duration
	// NoAutoName never starts the agent that names a session (SessionSpec.AutoName): for fakes that script each start.
	NoAutoName bool
}

// The host's parts, by the names it embeds them under.
type (
	logsPart    = persist.Logs
	statsPart   = persist.Stats
	reposPart   = repos.Registry
	runPart     = agentrun.Runner
	boxesPart   = sandboxhost.Boxes
	codePart    = github.GitHub
	jobsPart    = jobs.Jobs
	utilityPart = utility.Utilities
	reviewsPart = aireview.Reviews
	workPart    = worktree.Worktrees
)

// Host hosts sessions.
type Host struct {
	opts  Options
	store *eventstore.Store
	log   *slog.Logger
	env   hostcore.Env
	kinds kinds
	edit  sync.Mutex // one read-modify-write of a session's state at a time (State.Mutate)

	// The parts whose calls are the host's own, embedded: their methods are the host's, each recovering a panic.
	*logsPart
	*statsPart
	*reposPart
	*runPart
	*boxesPart
	*codePart
	*jobsPart
	*utilityPart
	*reviewsPart
	*workPart

	prices *pricing.Pricing

	closed   chan struct{}
	going    hostcore.Jobs // the env's goroutines (hostcore.Env.Go)
	stopOnce sync.Once
	closeErr error
}

// New starts a host: it reads back every session it had.
func New(opts Options) (*Host, error) {
	if err := defaults(&opts); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(opts.Home, "sessions"), 0o700); err != nil {
		return nil, err
	}
	w := &Host{opts: opts, store: eventstore.New(), log: opts.Log, closed: make(chan struct{})}
	w.env = hostcore.Env{Home: opts.Home, Log: opts.Log, Clock: opts.Clock, Closed: w.closed, Work: &w.going}
	if err := w.parts(); err != nil {
		return nil, err
	}
	if err := w.logsPart.Restore(w.store); err != nil {
		return nil, err
	}
	strip := persist.NewStrip(opts.Home)
	if err := strip.Restore(w.store); err != nil {
		w.log.Warn("the tab strip as it was left", "err", err)
	}
	w.statsPart.RestoreUsage()
	w.store.Subscribe(core.Filter{}, w.logsPart.Record)
	strip.Track(w.store)
	w.settle()
	_ = w.emit(core.MachineChanged{Machine: w.machineState()})
	w.reposPart.Changed()
	w.runPart.RulesChanged()
	w.resumeHandedOver()
	return w, nil
}

func defaults(opts *Options) error {
	if opts.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		opts.Home = filepath.Join(home, ".oriel")
	}
	if opts.Providers == nil {
		opts.Providers = &agent.Providers{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	opts.Clock = clock.Or(opts.Clock)
	if opts.Load == nil {
		opts.Load = sysload.NewReader().Read
	}
	if opts.OrielBin == "" {
		opts.OrielBin, _ = os.Executable()
	}
	if opts.ReposDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opts.ReposDir = filepath.Join(home, "src")
		}
	}
	return nil
}

// parts makes the host's parts, each given what it needs of the others.
func (w *Host) parts() error {
	st := state{w}
	w.logsPart = persist.NewLogs(w.env, st)
	w.prices = pricing.New(w.env, pricing.Deps{Client: func() *http.Client { return w.opts.HTTP },
		URL: func() string { return w.opts.PricesURL }})
	w.statsPart = persist.NewStats(w.env, st, w.opts.Providers, w.prices.Book)
	var err error
	if w.reposPart, err = repos.New(w.env, st, func() string { return w.opts.ReposDir }); err != nil {
		return err
	}
	w.workPart = worktree.New(w.env, st, worktree.Deps{Kinds: &w.kinds, Resolve: w.reposPart.Resolve, AddRepo: w.reposPart.Add,
		Repos: w.reposPart.Repos, ReposDir: w.reposPart.Dir, NotesFiles: w.notesFiles})
	w.runPart = agentrun.New(w.env, st, agentrun.Deps{Providers: w.opts.Providers, Kinds: &w.kinds,
		MCPServers:   w.mcpServers,
		Exec:         func() agent.Exec { return w.opts.Exec },
		Sandbox:      func() hostcore.SandboxFactory { return w.opts.Sandbox },
		Packs:        w.opts.Packs,
		KeyWait:      func() time.Duration { return w.opts.KeyWait },
		Repos:        w.reposPart.Repos,
		AddRepo:      w.AddRepo,
		AccountOf:    w.statsPart.AccountOf,
		Record:       w.statsPart.Record,
		UsageSampled: w.statsPart.UsageSampled,
		PROpen:       func(id core.SessionID, project, branch string) bool { return w.codePart.PROpen(id, project, branch) },
		RenameBranch: func(ctx context.Context, id core.SessionID, project, name string) error {
			return w.ShipRename(ctx, RenameReq{Session: id, Project: project, Name: name})
		},
		NewBranch: func(ctx context.Context, id core.SessionID, project, name string, carry bool) (string, error) {
			return w.NewBranch(ctx, NewBranchReq{Session: id, Project: project, Name: name, Carry: carry})
		},
		MachineChanged: func() { _ = w.emit(core.MachineChanged{Machine: w.machineState()}) },
		Prices:         w.prices})
	if w.boxesPart, err = sandboxhost.New(w.sandboxDeps()); err != nil {
		return err
	}
	w.codePart = github.New(w.env, st, github.Deps{Kinds: &w.kinds, Keyed: w.runPart.Keyed, Wake: w.runPart.Wake,
		Client:        func() func(string, []string) gh.Client { return w.opts.GitHub },
		HTTP:          func() *http.Client { return w.opts.HTTP },
		BranchRenamed: worktree.BranchRenamed})
	w.jobsPart = jobs.New(w.env, st, jobs.Deps{Providers: w.opts.Providers, Start: w.Start,
		Sandboxed: func() bool { return w.opts.Sandbox != nil && w.boxesPart.Config().Default != "host" }})
	w.utilityPart = utility.New(w.env, utility.Deps{Providers: w.opts.Providers, Exec: func() agent.Exec { return w.opts.Exec },
		Tools: w.runPart.AppTools, SignedOut: w.runPart.SignedOut})
	w.reviewsPart = aireview.New(w.env, st, aireview.Deps{Providers: w.opts.Providers, Exec: func() agent.Exec { return w.opts.Exec },
		Pack: func(name string) (agent.Pack, bool) {
			if w.opts.Packs == nil {
				return agent.Pack{}, false
			}
			return w.runPart.Pack(name)
		},
		Protected: w.runPart.Protected, SignedOut: w.runPart.SignedOut})
	return nil
}

// sandboxDeps are what the host's sandboxes need of it.
func (w *Host) sandboxDeps() sandboxhost.Deps {
	return sandboxhost.Deps{Env: w.env, State: state{w},
		Keyed: func(ctx context.Context, id core.SessionID, what, project string, fn func(ctx context.Context) error) error {
			return w.runPart.Keyed(ctx, id, what, project, fn)
		},
		Config:          func() sandbox.Config { return w.boxesPart.Config() },
		Providers:       w.opts.Providers.List,
		Factory:         func() hostcore.SandboxFactory { return w.opts.Sandbox },
		Live:            func(id core.SessionID) hostcore.Sandbox { return w.runPart.Sandbox(id) },
		Running:         func() []hostcore.Sandbox { return w.runPart.Sandboxes() },
		Repos:           func() []core.Repo { return w.reposPart.Repos() },
		AddRepo:         w.AddRepo,
		Note:            func(id core.SessionID, it core.Item) { w.runPart.AppendItem(id, it) },
		SetAgent:        func(id core.SessionID, fn func(a *core.AgentState)) { w.runPart.SetAgent(id, fn) },
		RestartWhenIdle: w.runPart.RestartWhenIdle}
}

// settle puts back what the last process left running when it ended without handing over.
func (w *Host) settle() {
	w.runPart.Settle()
	for _, se := range w.store.State().Sessions {
		w.reviewsPart.Settle(se)
	}
}

// HandOver stops every agent for the next start to resume, then closes the host: Oriel quitting.
func (w *Host) HandOver() {
	defer w.env.Recover("HandOver", nil)
	w.runPart.HandOver()
	w.Close()
}

// Store is the session host's state, which the app shares.
func (w *Host) Store() core.Store { return w.store }

// Close stops every agent, without handing them over, and closes the logs. Only the first call does anything.
func (w *Host) Close() error {
	w.stopOnce.Do(func() {
		defer w.env.Recover("Close", &w.closeErr)
		close(w.closed)
		w.runPart.Close()
		w.waitGoing()
		w.statsPart.Close()
		w.closeErr = errors.Join(w.closeErr, w.logsPart.Close())
	})
	return w.closeErr
}

// closeWait is how long closing waits for the host's goroutines by default.
const closeWait = 5 * time.Second

// waitGoing waits for the reviews and the env's goroutines to end, as they do once the host closes, but only for
// CloseWait: one that doesn't is logged by name and left, so a quit never hangs on it.
func (w *Host) waitGoing() {
	done := make(chan struct{})
	go func() {
		w.reviewsPart.Wait()
		w.going.Wait()
		close(done)
	}()
	t := time.NewTimer(cmp.Or(w.opts.CloseWait, closeWait))
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		w.log.Warn("closing without waiting any longer", "still running", strings.Join(w.going.Running(), ", "))
	}
}

// lastingJobs are the host's jobs that run as long as what they serve, so never end on their own.
var lastingJobs = []string{"an agent's events", "a utility agent", "a permission", "a question", "Terminal", "a terminal dialog",
	"a sign-in", "usage", "a stuck fetch"}

// notesFiles are the files the providers' agents read a workspace's notes from (agent.NotesFiles).
func (w *Host) notesFiles() []string { return agent.NotesFiles(w.opts.Providers.List()) }

// Busy names the host's work under way off its callers that will end on its own: agents starting, resumes,
// notices. A test waits for none before it counts what that work does.
func (w *Host) Busy() []string {
	out := w.runPart.Starting()
	for _, j := range w.going.Running() {
		if name, _, _ := strings.Cut(j, " ×"); !slices.Contains(lastingJobs, name) {
			out = append(out, j)
		}
	}
	return out
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

func (w *Host) restatus(id core.SessionID) { hostcore.Restatus(state{w}, id) }

// state is the host as its parts see it.
type state struct{ w *Host }

func (s state) Snapshot() core.State { return s.w.store.State() }

func (s state) Session(id core.SessionID) (core.SessionState, error) { return s.w.session(id) }

func (s state) Emit(ev core.Event) error { return s.w.emit(ev) }

func (s state) Mutate(id core.SessionID, fn func(se core.SessionState) error) error {
	s.w.edit.Lock()
	defer s.w.edit.Unlock()
	se, err := s.w.session(id)
	if err != nil {
		return err
	}
	return fn(se)
}

// mcpServers are the MCP servers a session's agent reaches of its own: in a sandbox, those its gateway carries; on
// this machine, the ones installed for its provider.
func (w *Host) mcpServers(se core.SessionState) []agent.MCPServer {
	if se.Agent.Sandboxed && w.boxesPart != nil {
		return w.boxesPart.MCPServers()
	}
	if w.opts.Providers == nil {
		return nil
	}
	if p, ok := w.opts.Providers.Get(se.Agent.Provider); ok {
		if s, ok := p.(agent.MCPInstalled); ok {
			return s.MCPServers()
		}
	}
	return nil
}
