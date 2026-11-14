// Package agentrun runs sessions' agents: it starts and stops each, hands it the user's comments and messages a turn
// at a time, puts its permission asks and questions to the session's rules, the app's hooks and the user, gives it
// Oriel's own tools, and turns what it does into the session's events: its transcript, threads, tasks and evidence.
package agentrun

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/agent/usage"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/testrun"
)

// Runner runs the agents of the host's sessions.
type Runner struct {
	env  hostcore.Env
	st   hostcore.State
	deps Deps

	mu          sync.Mutex
	live        map[core.SessionID]*session // the sessions with an agent running or starting
	app         App
	prompts     prompts.Overrides // the user's versions of the prompts the app last sent
	promptsSent bool
	starts      hostcore.Jobs // agents starting off their callers (startAside), and restarts

	loginMu sync.Mutex
	lost    map[string]*lostLogin // logins gone, by provider and account

	resumed    map[core.SessionID]bool // the sessions handed over that ResumeHandedOver has resumed
	keepExited bool                    // an agent that exits on its own stays stopped

	rules *allRules // for all sessions

	testsOnce sync.Once
	testSet   *testrun.Set
}

// Deps are what running an agent needs of the rest of the host.
type Deps struct {
	Providers *agent.Providers
	Kinds     hostcore.Kinds
	// MCPServers are the MCP servers a session's agent reaches of its own: its provider's, or in a sandbox those
	// its gateway carries. Nil for none.
	MCPServers func(se core.SessionState) []agent.MCPServer
	// Exec is how agents' processes start.
	Exec agent.Exec
	// Sandbox makes a sandboxed session's sandbox; nil when this machine can't run one.
	Sandbox hostcore.SandboxFactory
	// Packs finds a kind's prompt pack, before the user's versions; nil for none.
	Packs func(name string) (agent.Pack, bool)
	// KeyWait is how long a git operation runs before its session asks for a security-key touch; never if negative.
	KeyWait time.Duration
	// Repos are the registered checkouts, which an agent never edits.
	Repos func() []core.Repo
	// AddRepo brings a registered repo, by name or path, into a session as a project of its own.
	AddRepo func(ctx context.Context, id core.SessionID, ref core.RepoID) error
	// AccountOf is the account a provider is signed in to.
	AccountOf func(provider string) (string, string)
	// Record keeps an agent event in its session's record, for a provider with no log of its own.
	Record func(se core.SessionState, ev usage.Event)
	// UsageSampled reports a plan reading, and keeps it.
	UsageSampled func(u core.ProviderUsage)
	// RenameBranch renames the session's branch for a project.
	RenameBranch func(ctx context.Context, id core.SessionID, project, name string) error
	// NewBranch moves a project onto a fresh branch off its default branch, which the session takes, and answers its
	// name: name when it isn't "". Uncommitted work is refused unless carry.
	NewBranch func(ctx context.Context, id core.SessionID, project, name string, carry bool) (string, error)
	// PROpen says the last reading of a project's branch found its pull request open.
	PROpen func(id core.SessionID, project, branch string) bool
	// MachineChanged reports the machine's state again: its lost logins have changed.
	MachineChanged func()
	// Prices are what agents' turns are priced with.
	Prices price.Books
}

// New runs st's sessions' agents.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Runner {
	r := &Runner{env: env, st: st, deps: deps, live: map[core.SessionID]*session{}, rules: &allRules{path: filepath.Join(env.Home, "rules.json")}}
	r.loadPrompts()
	if err := r.rules.load(); err != nil {
		env.Log.Warn("rules for all sessions", "path", r.rules.path, "err", err)
	}
	return r
}

func (x *Runner) sessionFor(id core.SessionID, start bool) (*session, error) {
	if !start {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.live[id], nil
	}
	h := x.sessionOf(id)
	return h, h.start()
}

// sessionOf is session id's runner, made if it has none, without starting its agent.
func (x *Runner) sessionOf(id core.SessionID) *session {
	x.mu.Lock()
	defer x.mu.Unlock()
	h := x.live[id]
	if h == nil {
		h = &session{r: x, id: id, ctx: context.Background(), calls: map[string]agent.ToolCall{}, items: map[string]core.Item{},
			waiting: map[string]chan AnswerReq{}, nudged: map[string]string{}}
		x.live[id] = h
	}
	return h
}

// all are the sessions with an agent running or starting.
func (x *Runner) all() []*session {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]*session, 0, len(x.live))
	for _, h := range x.live {
		out = append(out, h)
	}
	return out
}

func (x *Runner) mutateThread(id core.SessionID, tid core.ThreadID, fn func(t *core.Thread) error) (core.Thread, error) {
	return hostcore.MutateThread(x.st, x.env.Now(), id, tid, fn)
}

// HandOver stops every agent for the next start to resume: Oriel quitting.
func (x *Runner) HandOver() {
	var wg sync.WaitGroup
	for _, h := range x.all() {
		// Together: each stop takes its sandbox down, which takes a while.
		wg.Go(func() {
			defer x.env.Recover("HandOver", nil)
			h.stop("Oriel quit", core.StoppedByHandOver)
		})
	}
	wg.Wait()
}

// Close stops every agent, without handing them over, and waits for those starting.
func (x *Runner) Close() {
	for _, h := range x.all() {
		h.stop("the session host closed", "")
	}
	x.starts.Wait()
}

// Starting are the agents starting off their callers, and restarting, not up yet: "starting an agent ×2".
func (x *Runner) Starting() []string { return x.starts.Running() }

// Resume starts session id's agent if it isn't running, by its own id when it had one so the conversation carries
// on, and hands it what's waiting. One running that has stopped answering is started again.
func (x *Runner) Resume(id core.SessionID) error {
	h := x.sessionOf(id)
	if h.unresponsive() {
		h.stop("it stopped answering", "")
	}
	h.forgetExits()
	if err := h.start(); err != nil {
		return err
	}
	h.kick()
	return nil
}

// Stop stops session id's agent, if it runs; by is who did (core.StoppedByYou…), "" for a stop that isn't one.
func (x *Runner) Stop(id core.SessionID, why, by string) {
	if h, _ := x.sessionFor(id, false); h != nil {
		h.stop(why, by)
	}
}

// Forget drops a deleted session's runner.
func (x *Runner) Forget(id core.SessionID) {
	x.mu.Lock()
	delete(x.live, id)
	x.mu.Unlock()
}

// Wake hands session id's agent what's waiting for it, starting it if it isn't running.
func (x *Runner) Wake(id core.SessionID) {
	if h, err := x.sessionFor(id, true); err == nil {
		h.kick()
	}
}

// StartAside starts session id's agent off the caller: a failure is in the agent's state, and logged.
func (x *Runner) StartAside(id core.SessionID) { x.sessionOf(id).startAside() }

// Note is something to tell session id's agent with its next batch, if it has one: a repo joined, say.
func (x *Runner) Note(id core.SessionID, text string) {
	if h, _ := x.sessionFor(id, false); h != nil {
		h.note(text)
	}
}

// AppendItem puts an item in session id's transcript while its agent runs.
func (x *Runner) AppendItem(id core.SessionID, it core.Item) {
	if h, _ := x.sessionFor(id, false); h != nil {
		h.appendItem(it)
	}
}

// SetAgent changes session id's agent state while it runs.
func (x *Runner) SetAgent(id core.SessionID, fn func(a *core.AgentState)) {
	if h, _ := x.sessionFor(id, false); h != nil {
		h.setAgent(fn)
	}
}

// Sandbox is session id's sandbox while its agent runs in one.
func (x *Runner) Sandbox(id core.SessionID) hostcore.Sandbox {
	h, _ := x.sessionFor(id, false)
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.box
}

// Sandboxes are the sandboxes agents run in now.
func (x *Runner) Sandboxes() []hostcore.Sandbox {
	var out []hostcore.Sandbox
	for _, h := range x.all() {
		h.mu.Lock()
		if h.box != nil {
			out = append(out, h.box)
		}
		h.mu.Unlock()
	}
	return out
}

// Running says session id's agent is running.
func (x *Runner) Running(id core.SessionID) bool {
	h, _ := x.sessionFor(id, false)
	return h != nil && h.running()
}

// RestartWhenIdle restarts session id's agent, resumed, if it still runs in sb: now if it's idle, else once its turn
// ends, before it's handed anything more.
func (x *Runner) RestartWhenIdle(id core.SessionID, sb hostcore.Sandbox, why string) {
	h, _ := x.sessionFor(id, false)
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.box != sb {
		h.mu.Unlock()
		return
	}
	working := h.working
	if working {
		h.restartWhy = why
	}
	h.mu.Unlock()
	if !working {
		h.restart(why)
	}
}

// ResumeHandedOver starts again the agents the last process handed over, or had running when it crashed, once each
// and only once its provider is known: it's called again as providers arrive. An archived session's stays stopped.
func (x *Runner) ResumeHandedOver(resume func(id core.SessionID) error) {
	for _, se := range x.st.Snapshot().Sessions {
		if by := se.Agent.StoppedBy; !se.Archived && !se.Agent.Running && (by == core.StoppedByHandOver || by == core.StoppedByCrash) {
			if !x.claimResume(se) {
				continue
			}
			x.env.Go("resume", func() {
				if err := resume(se.ID); err != nil {
					x.env.Log.Warn("resume after the last run", "session", se.ID, "by", by, "err", err)
				}
			})
		}
	}
}

// claimResume says se's agent is to be resumed now: its provider is known, and it hasn't been already.
func (x *Runner) claimResume(se core.SessionState) bool {
	if _, ok := x.deps.Providers.Get(se.Agent.Provider); !ok && se.Agent.Provider != "" {
		return false
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.resumed[se.ID] {
		return false
	}
	if x.resumed == nil {
		x.resumed = map[core.SessionID]bool{}
	}
	x.resumed[se.ID] = true
	return true
}

// Settle puts back what the last process left running when it ended without handing over: those agents stopped
// with it (StoppedByCrash), and nobody is asked anything: its agents' asks are closed, and its questions dismissed.
func (x *Runner) Settle() {
	for _, n := range x.st.Snapshot().Notifications {
		if n.Session != "" && n.Severity == core.SevQuestion && !n.Dismissed {
			_ = x.st.Emit(core.Dismissal(n))
		}
	}
	for _, se := range x.st.Snapshot().Sessions {
		if se.Agent.Running || se.Agent.Working || se.Agent.Starting != nil || len(se.Asks) > 0 {
			a := se.Agent
			a.Stopped(core.StoppedByCrash, x.env.Now())
			_ = x.st.Emit(core.AgentChanged{ID: se.ID, Agent: a})
			for _, ask := range se.Asks {
				_ = x.st.Emit(core.AskClosed{Session: se.ID, ID: ask.ID, Decision: "gone"})
			}
			hostcore.Restatus(x.st, se.ID)
		}
	}
}
