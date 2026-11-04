package agentrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// session runs one session's agent: it starts and stops it, hands it comments, and turns what it does into events.
type session struct {
	r   *Runner
	id  core.SessionID
	ctx context.Context

	mu           sync.Mutex
	cancel       context.CancelFunc
	sess         agent.Session
	box          hostcore.Sandbox
	starting     bool
	working      bool // a turn is running, or one was just asked for
	interrupting bool
	relogin      bool // a turn stopped for a lost login: told to carry on once it's back
	stale        bool // the agent lost what only a restart brings back: restarted once its turn ends
	refused      bool // the agent couldn't start for a lost login: started once it's back
	loginRetried bool // a sandboxed agent's lost sign-in was tried again with the host's, since its last turn that worked
	notes        []string
	delivered    []core.ThreadID // the threads the turn now running was given
	handing      map[string]bool // the comments of the first turn, handed over in the agent's start: its once it can answer
	held         []core.ThreadID // threads given to turns that left background work running: busy until it's done
	runs         []string        // the turn's test runs
	model        string          // the model the agent last ran as, by its tool calls: "claude-opus-5-5"
	calls        map[string]agent.ToolCall
	items        map[string]core.Item
	waiting      map[string]chan AnswerReq // asks the user hasn't answered
	turnAt       time.Time
	drafts       bool                    // its pack fills the Ship page's drafts (commit_draft)
	nudged       map[string]string       // project/kind → the code it was last asked to draft for
	carriedAt    string                  // what it owed when last told to carry on
	carried      int                     // times told to carry on since the user last wrote
	trees        map[string]string       // each project's work tree at the last mark, by directory
	markq        []func([]core.FileDiff) // marks waiting to run, each with what to do with its changes
	marking      bool                    // a goroutine is running markq
}

func (h *session) running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sess != nil
}

// start starts the agent if it isn't running: resuming its conversation when it has had one.
func (h *session) start() error {
	h.mu.Lock()
	if h.sess != nil || h.starting {
		h.mu.Unlock()
		return nil
	}
	if h.r.env.IsClosed() {
		h.mu.Unlock()
		return errors.New("the session host has closed")
	}
	h.starting = true
	since := h.r.env.Now().UTC()
	ctx, cancel := context.WithCancel(sandbox.WithProgress(context.Background(), func(p sandbox.Progress) {
		h.setAgent(func(a *core.AgentState) {
			if a.Starting != nil {
				a.Starting = &core.AgentStart{Step: p.Step, Note: p.Note, Since: since}
			}
		})
	}))
	h.cancel = cancel
	h.mu.Unlock()
	h.setAgent(func(a *core.AgentState) { a.Starting = &core.AgentStart{Step: "Starting the agent", Since: since} })
	resumed := h.snapshot().Agent.Session != ""
	var first handout
	if !resumed {
		first = h.firstTurn()
	}
	h.mu.Lock()
	h.handing = first.comments
	h.mu.Unlock()
	sess, box, err := h.launch(ctx, cancel, first.text)
	h.mu.Lock()
	h.starting, h.handing = false, nil                          // given below, with h.mu held, when it started
	if err == nil && (ctx.Err() != nil || h.r.env.IsClosed()) { // stopped while it started
		h.mu.Unlock()
		_ = sess.Close()
		if box != nil {
			_ = box.Close()
		}
		return errors.New("the agent was stopped as it started")
	}
	if err != nil {
		h.mu.Unlock()
		cancel()
		h.setAgent(func(a *core.AgentState) { a.Running, a.Working, a.Starting, a.Error = false, false, nil, err.Error() })
		h.refusedForLogin(err)
		return err
	}
	h.sess, h.box = sess, box
	if first.text != "" {
		_ = h.r.st.Mutate(h.id, func(se core.SessionState) error {
			h.give(se, first)
			return nil
		})
	}
	// Running is set with h.mu held, so a stop that takes h.sess can't land before it and be undone by it.
	acct, name := h.r.deps.AccountOf(h.snapshot().Agent.Provider)
	now := h.r.env.Now().UTC()
	h.setAgent(func(a *core.AgentState) {
		a.Running, a.Starting, a.Error, a.StoppedBy, a.StoppedAt = true, nil, "", "", time.Time{}
		a.RanOn(now, acct, name)
	})
	h.mu.Unlock()
	if first.text != "" {
		hostcore.Restatus(h.r.st, h.id)
	}
	if resumed {
		// After "The agent exited", the transcript says it's back.
		h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemSystem, Time: now, Text: "The agent started again, resuming its conversation"})
	}
	h.r.env.Go("an agent's events", func() { h.loop(sess) })
	h.askLogins(h.snapshot().Agent.Provider)
	h.kick()
	if resumed {
		h.mu.Lock()
		idle := !h.working
		h.carriedAt, h.carried = "", 0
		h.mu.Unlock()
		if idle {
			h.carryOn(resumedLead)
		}
	}
	return nil
}

// firstTurn is what a fresh agent's first turn carries: what's waiting for it, so it starts on it rather than on a
// turn of its skill alone with the request behind it.
func (h *session) firstTurn() handout {
	h.mu.Lock()
	defer h.mu.Unlock()
	var t handout
	_ = h.r.st.Mutate(h.id, func(se core.SessionState) error {
		if !h.r.SignedOut(se.Agent.Provider) {
			t, _ = h.next(se)
		}
		return nil
	})
	return t
}

// startAside starts the agent off the caller: a failure is in the agent's state, and logged. Close waits for it.
func (h *session) startAside() {
	h.r.starts.Go(func() {
		defer h.r.env.Recover("starting an agent", nil)
		if err := h.start(); err != nil {
			h.r.env.Log.Warn("the agent didn't start", "session", h.id, "err", err)
		}
	})
}

// launch starts the agent under ctx, which cancel ends if it fails, with prompt as its first turn.
func (h *session) launch(ctx context.Context, cancel context.CancelFunc, prompt string) (agent.Session, hostcore.Sandbox, error) {
	x := h.r
	se, err := x.st.Session(h.id)
	if err != nil {
		return nil, nil, err
	}
	p, ok := x.deps.Providers.Get(se.Agent.Provider)
	if !ok {
		return nil, nil, fmt.Errorf("no agent provider %q", se.Agent.Provider)
	}
	spec := agent.StartSpec{Cwd: hostcore.AgentDir(se), Model: se.Agent.Model, Effort: se.Agent.Effort, Policy: agent.Policy(se.Agent.Policy),
		Tools: x.tools(h), Protect: x.Protected(se), Exec: x.deps.Exec(), StateDir: filepath.Join(x.env.SessionDir(h.id), "agent"),
		Env: []string{"ORIEL_SESSION=" + string(h.id)}, Prompt: prompt, Prices: x.deps.Prices}
	if m := hostcore.Memories(se); len(m) > 0 {
		spec.Memory = m[0].Dir
	}
	if k, ok := x.deps.Kinds.Get(se.Kind); ok {
		name := k.Pack
		if se.Agent.Pack != "" {
			name = se.Agent.Pack
		}
		if pack, ok := x.Pack(name); ok {
			spec.Pack = pack
		}
	}
	x.WriteNotes(h.id)
	h.mu.Lock()
	h.drafts = slices.Contains(spec.Pack.Tools, "commit_draft")
	h.mu.Unlock()
	var box hostcore.Sandbox
	if se.Agent.Sandboxed {
		if x.deps.Sandbox() == nil {
			cancel()
			return nil, nil, errors.New("this machine can't run a sandbox")
		}
		box, err = x.deps.Sandbox().Open(ctx, se, h.approve, hostcore.SandboxAgent{Provider: p, StateDir: spec.StateDir})
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("sandbox: %w", err)
		}
		spec.Exec, spec.Sandbox = box.Exec(), box.Spec()
	}
	if spec.Exec == nil {
		cancel()
		return nil, nil, errors.New("there is no way to start an agent")
	}
	var sess agent.Session
	if se.Agent.Session != "" {
		sess, err = p.Resume(ctx, se.Agent.Session, spec)
	} else {
		sess, err = p.Start(ctx, spec)
	}
	if err != nil {
		cancel()
		if box != nil {
			box.Close()
		}
		return nil, nil, err
	}
	return sess, box, nil
}

// Protected are the user's own checkouts, which the agent must never edit: it works in the worktrees.
func (x *Runner) Protected(se core.SessionState) []string {
	if se.InPlace() {
		return nil
	}
	var out []string
	for _, p := range se.Projects {
		if p.Repo != "" && !slices.Contains(out, p.Repo) {
			out = append(out, p.Repo)
		}
	}
	for _, r := range x.deps.Repos() {
		if !slices.Contains(out, r.Path) {
			out = append(out, r.Path)
		}
	}
	return out
}

func (h *session) ranModel() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.model
}

// restart stops the agent and starts it again on its conversation, off the caller.
func (h *session) restart(why string) {
	h.r.starts.Go(func() {
		defer h.r.env.Recover("restarting an agent", nil)
		h.stop(why, "")
		if err := h.start(); err != nil {
			h.r.env.Log.Warn("the agent didn't start again", "session", h.id, "why", why, "err", err)
		}
	})
}

// unresponsive says the agent runs but its session has stopped hearing from it.
func (h *session) unresponsive() bool {
	h.mu.Lock()
	r, ok := h.sess.(agent.Responsive)
	h.mu.Unlock()
	return ok && !r.Responsive()
}

// stop stops the agent, and its threads wait for it to come back; by is who did (core.StoppedByYou…), "" for a stop that isn't one: a restart, a delete.
func (h *session) stop(why, by string) {
	h.mu.Lock()
	sess, box, cancel := h.sess, h.box, h.cancel
	h.sess, h.box, h.cancel, h.working = nil, nil, nil, false
	waiting := h.waiting
	h.waiting = map[string]chan AnswerReq{}
	h.mu.Unlock()
	for id, ch := range waiting {
		close(ch)
		_ = h.r.st.Emit(core.AskClosed{Session: h.id, ID: id, Decision: "gone"})
	}
	if sess != nil {
		_ = sess.Close()
	}
	if box != nil {
		_ = box.Close()
	}
	if cancel != nil {
		cancel()
	}
	h.setAgent(func(a *core.AgentState) { a.Stopped(by, h.r.env.Now()) })
	h.r.env.Log.Info("agent stopped", "session", h.id, "why", why)
}

func (h *session) setAgent(fn func(a *core.AgentState)) {
	err := h.r.st.Mutate(h.id, func(se core.SessionState) error {
		a := se.Agent
		a.Queue = slices.Clone(a.Queue)
		fn(&a)
		_ = h.r.st.Emit(core.AgentChanged{ID: h.id, Agent: a})
		return nil
	})
	if err == nil {
		hostcore.Restatus(h.r.st, h.id)
	}
}

// note is something to tell the agent with the next batch: a repo joined, say.
func (h *session) note(text string) {
	h.mu.Lock()
	h.notes = append(h.notes, text)
	h.mu.Unlock()
	h.kick()
}

// urgent interrupts the running turn, so the next batch goes first.
func (h *session) urgent() {
	h.mu.Lock()
	sess, working := h.sess, h.working
	if working {
		h.interrupting = true
	}
	h.mu.Unlock()
	if sess != nil && working {
		_ = sess.Interrupt(h.ctx)
	}
}
