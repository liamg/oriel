package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/sandbox"
)

// SandboxFactory makes the sandbox, gateway and egress proxy a sandboxed session runs in (AI-11). Layer-3
// approvals (a held connection, a held gateway call) come back through approve, which shows the user a card
// and waits for the answer.
type SandboxFactory interface {
	Open(ctx context.Context, se core.SessionState, approve Approve) (Sandbox, error)
	// Backends are the sandbox backends this machine can run.
	Backends() []string
}

// Approve puts an ask to the session's rules, the app's hooks, then the user, and waits for the answer.
type Approve func(ctx context.Context, ask core.Ask) (Approval, error)

// Approval is an answer to an ask, with who gave it and what they said, for a refusal the agent reads.
type Approval struct {
	Allow bool
	Scope string // a wider rule the user chose ("*.golang.org"), or ""
	By    string // "the user", "rule net(…)", the hook's source
	Note  string
}

// Sandbox is one session's sandbox.
type Sandbox interface {
	Exec() agent.Exec
	Spec() *agent.Sandbox
	Close() error
}

// host runs one session's agent: it starts and stops it, hands it comments, and turns what it does into events.
type session struct {
	w   *Host
	id  core.SessionID
	ctx context.Context

	mu           sync.Mutex
	cancel       context.CancelFunc
	sess         agent.Session
	box          Sandbox
	starting     bool
	working      bool // a turn is running, or one was just asked for
	interrupting bool
	relogin      bool // a turn stopped for a lost login: told to carry on once it's back
	refused      bool // the agent couldn't start for a lost login: started once it's back
	notes        []string
	delivered    []core.ThreadID // the threads the turn now running was given
	held         []core.ThreadID // threads given to turns that left background work running: busy until it's done
	activity     []core.Activity // what the agent did since it last replied
	runs         []string        // test runs since it last replied
	calls        map[string]agent.ToolCall
	items        map[string]core.Item
	waiting      map[string]chan AnswerReq // asks the user hasn't answered
	turnAt       time.Time
	drafts       bool              // its pack fills the Ship page's drafts (commit_draft)
	nudged       map[string]string // project/kind → the code it was last asked to draft for
	carriedAt    string            // what it owed when last told to carry on
	carried      int               // times told to carry on since the user last wrote
}

func (w *Host) sessionFor(id core.SessionID, start bool) (*session, error) {
	if !start {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.live[id], nil
	}
	h := w.sessionOf(id)
	return h, h.start()
}

// sessionOf is session id's host, made if it has none, without starting its agent.
func (w *Host) sessionOf(id core.SessionID) *session {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := w.live[id]
	if h == nil {
		h = &session{w: w, id: id, ctx: context.Background(), calls: map[string]agent.ToolCall{}, items: map[string]core.Item{},
			waiting: map[string]chan AnswerReq{}, nudged: map[string]string{}}
		w.live[id] = h
	}
	return h
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
	h.starting = true
	since := h.w.opts.Now().UTC()
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
	sess, box, err := h.launch(ctx, cancel, first.text)
	h.mu.Lock()
	h.starting = false
	if err == nil && ctx.Err() != nil { // stopped while it started
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
		h.w.edit.Lock()
		if se, err := h.w.session(h.id); err == nil {
			h.give(se, first)
		}
		h.w.edit.Unlock()
	}
	h.mu.Unlock()
	if first.text != "" {
		h.w.restatus(h.id)
	}
	acct, name := h.w.accountOf(h.snapshot().Agent.Provider)
	now := h.w.opts.Now().UTC()
	h.setAgent(func(a *core.AgentState) {
		a.Running, a.Starting, a.Error, a.StoppedBy, a.StoppedAt = true, nil, "", "", time.Time{}
		a.RanOn(now, acct, name)
	})
	if resumed {
		// After "The agent exited", the transcript says it's back.
		h.appendItem(core.Item{ID: newID(6), Kind: core.ItemSystem, Time: now, Text: "The agent started again, resuming its conversation"})
	}
	h.w.goSafe("an agent's events", func() { h.loop(sess) })
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
	h.w.edit.Lock()
	defer h.w.edit.Unlock()
	se, err := h.w.session(h.id)
	if err != nil || h.w.signedOut(se.Agent.Provider) {
		return handout{}
	}
	t, _ := h.next(se)
	return t
}

// startAside starts the agent off the caller: a failure is in the agent's state, and logged. Close waits for it.
func (h *session) startAside() {
	h.w.starts.Go(func() {
		defer h.w.recover("starting an agent", nil)
		if err := h.start(); err != nil {
			h.w.log.Warn("the agent didn't start", "session", h.id, "err", err)
		}
	})
}

// launch starts the agent under ctx, which cancel ends if it fails, with prompt as its first turn.
func (h *session) launch(ctx context.Context, cancel context.CancelFunc, prompt string) (agent.Session, Sandbox, error) {
	w := h.w
	se, err := w.session(h.id)
	if err != nil {
		return nil, nil, err
	}
	p, ok := w.opts.Providers.Get(se.Agent.Provider)
	if !ok {
		return nil, nil, fmt.Errorf("no agent provider %q", se.Agent.Provider)
	}
	spec := agent.StartSpec{Cwd: agentDir(se), Model: se.Agent.Model, Effort: se.Agent.Effort, Policy: agent.Policy(se.Agent.Policy),
		Tools: w.tools(h), Protect: w.protected(se), Exec: w.opts.Exec, StateDir: filepath.Join(w.sessionDir(h.id), "agent"),
		Env: []string{"ORIEL_SESSION=" + string(h.id)}, Prompt: prompt}
	if k, ok := w.kinds.Get(se.Kind); ok {
		name := k.Pack
		if se.Agent.Pack != "" {
			name = se.Agent.Pack
		}
		if pack, ok := w.pack(name); ok {
			spec.Pack = pack
		}
	}
	w.writeNotes(h.id)
	h.mu.Lock()
	h.drafts = slices.Contains(spec.Pack.Tools, "commit_draft")
	h.mu.Unlock()
	var box Sandbox
	if se.Agent.Sandboxed {
		if w.opts.Sandbox == nil {
			cancel()
			return nil, nil, errors.New("this machine can't run a sandbox")
		}
		box, err = w.opts.Sandbox.Open(ctx, se, h.approve)
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

// agentDir is where a session's agent starts: the workspace, or the directory itself.
func agentDir(se core.SessionState) string {
	if se.Dir != "" {
		return se.Dir
	}
	if p, ok := se.Primary(); ok {
		return p.Dir
	}
	return ""
}

// protected are the user's own checkouts, which the agent must never edit: it works in the worktrees.
func (w *Host) protected(se core.SessionState) []string {
	if se.Kind == core.KindDirect {
		return nil
	}
	var out []string
	for _, p := range se.Projects {
		if p.Repo != "" && !slices.Contains(out, p.Repo) {
			out = append(out, p.Repo)
		}
	}
	for _, r := range w.repos.all() {
		if !slices.Contains(out, r.Path) {
			out = append(out, r.Path)
		}
	}
	return out
}

// stop ends the agent. Its threads wait for it to come back.
// stop stops the agent; by is who did (core.StoppedByYou…), "" for a stop that isn't one: a restart, a delete.
func (h *session) stop(why, by string) {
	h.mu.Lock()
	sess, box, cancel := h.sess, h.box, h.cancel
	h.sess, h.box, h.cancel, h.working = nil, nil, nil, false
	waiting := h.waiting
	h.waiting = map[string]chan AnswerReq{}
	h.mu.Unlock()
	for id, ch := range waiting {
		close(ch)
		_ = h.w.emit(core.AskClosed{Session: h.id, ID: id, Decision: "gone"})
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
	h.setAgent(func(a *core.AgentState) { a.Stopped(by, h.w.opts.Now()) })
	h.w.log.Info("agent stopped", "session", h.id, "why", why)
}

func (h *session) setAgent(fn func(a *core.AgentState)) {
	h.w.edit.Lock()
	se, err := h.w.session(h.id)
	if err != nil {
		h.w.edit.Unlock()
		return
	}
	a := se.Agent
	a.Queue = slices.Clone(a.Queue)
	fn(&a)
	_ = h.w.emit(core.AgentChanged{ID: h.id, Agent: a})
	h.w.edit.Unlock()
	h.w.restatus(h.id)
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

// kick hands the agent whatever is waiting for it, if it's free to take it.
func (h *session) kick() {
	h.mu.Lock()
	if h.sess == nil || h.working || h.starting {
		h.mu.Unlock()
		return
	}
	sess := h.sess
	h.w.edit.Lock()
	se, err := h.w.session(h.id)
	if err != nil || h.w.signedOut(se.Agent.Provider) {
		h.w.edit.Unlock()
		h.mu.Unlock()
		return
	}
	t, ok := h.next(se)
	if !ok {
		h.w.edit.Unlock()
		h.mu.Unlock()
		return
	}
	h.give(se, t)
	h.w.edit.Unlock()
	h.mu.Unlock()
	h.w.restatus(h.id)
	h.send(sess, agent.Input{Text: t.text}, t.threads)
}

// handout is what the agent is handed next: a queued message, or a batch of comments and notes.
type handout struct {
	text     string
	queued   bool            // text is the queue's first message
	threads  []core.ThreadID // the threads in the batch
	comments map[string]bool // the comments it gives the agent
	notes    int             // how many of h.notes it carries
}

// next is what's waiting for the agent in se: queued messages first, then a batch of comments. Callers hold h.mu.
func (h *session) next(se core.SessionState) (handout, bool) {
	if len(se.Agent.Queue) > 0 {
		return handout{text: se.Agent.Queue[0], queued: true}, true
	}
	var batch []core.Thread
	fresh := map[string]bool{}
	for _, t := range se.Threads {
		if !t.Open() || t.Btw {
			continue
		}
		cs := t.Undelivered()
		if len(cs) == 0 {
			continue
		}
		for _, c := range cs {
			fresh[c.ID] = true
		}
		batch = append(batch, t)
	}
	if len(batch) == 0 && len(h.notes) == 0 {
		return handout{}, false
	}
	// Urgent threads go first.
	slices.SortStableFunc(batch, func(a, b core.Thread) int {
		switch {
		case a.Urgent && !b.Urgent:
			return -1
		case b.Urgent && !a.Urgent:
			return 1
		}
		return 0
	})
	t := handout{text: renderBatch(se, batch, fresh, h.notes), comments: fresh, notes: len(h.notes)}
	for _, th := range batch {
		t.threads = append(t.threads, th.ID)
	}
	return t, true
}

// give marks t as the agent's, in se as it is now, and the agent as working on it. Callers hold h.mu and w.edit.
func (h *session) give(se core.SessionState, t handout) {
	if t.queued || len(t.comments) > 0 {
		h.carried = 0
	}
	if t.queued {
		a := se.Agent
		a.Queue = slices.Clone(a.Queue)
		if i := slices.Index(a.Queue, t.text); i >= 0 {
			a.Queue = slices.Delete(a.Queue, i, i+1)
		}
		_ = h.w.emit(core.AgentChanged{ID: h.id, Agent: a})
		h.working = true
		return
	}
	for _, tid := range t.threads {
		th, ok := se.Thread(tid)
		if !ok {
			continue
		}
		th.Comments = slices.Clone(th.Comments)
		for i := range th.Comments {
			if t.comments[th.Comments[i].ID] {
				th.Comments[i].Delivered = true
			}
		}
		th.Busy = true
		th.Updated = h.w.opts.Now().UTC()
		_ = h.w.emit(core.ThreadUpdated{Session: h.id, Thread: th})
	}
	h.notes = h.notes[min(t.notes, len(h.notes)):]
	h.delivered = t.threads
	h.working = true
}

// send gives the agent a turn; if that fails, what it carried waits for the next.
func (h *session) send(sess agent.Session, in agent.Input, threads []core.ThreadID) {
	if err := sess.Send(h.ctx, in); err != nil {
		h.mu.Lock()
		h.working = false
		h.mu.Unlock()
		for _, tid := range threads {
			_, _ = h.w.mutateThread(h.id, tid, func(t *core.Thread) error {
				t.Busy = false
				for i := range t.Comments {
					t.Comments[i].Delivered = t.Comments[i].Delivered && t.Comments[i].Author == core.AuthorAgent
				}
				return nil
			})
		}
		h.setAgent(func(a *core.AgentState) { a.Error = "couldn't reach the agent: " + err.Error() })
	}
}

// queue holds a message for the agent until its turn ends, or sends it now when it's free.
func (h *session) queue(text string, urgent bool) error {
	h.w.edit.Lock()
	se, err := h.w.session(h.id)
	if err != nil {
		h.w.edit.Unlock()
		return err
	}
	a := se.Agent
	if urgent {
		a.Queue = append([]string{text}, a.Queue...)
	} else {
		a.Queue = append(slices.Clone(a.Queue), text)
	}
	err = h.w.emit(core.AgentChanged{ID: h.id, Agent: a})
	h.w.edit.Unlock()
	if urgent {
		h.urgent()
	}
	h.kick()
	return err
}

// fork asks the agent a side question outside the conversation.
func (h *session) fork(prompt string) (string, error) {
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil {
		return "", errors.New("the agent isn't running")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()
	return sess.Fork(ctx, agent.ForkSpec{Prompt: prompt})
}

// loop turns the agent's events into the session's state until it exits.
func (h *session) loop(sess agent.Session) {
	for ev := range sess.Events() {
		h.handle(sess, ev)
	}
	h.mu.Lock()
	mine := h.sess == sess
	if mine {
		h.sess, h.working = nil, false
	}
	box := h.box
	if mine {
		h.box = nil
	}
	h.mu.Unlock()
	if mine {
		if box != nil {
			_ = box.Close()
		}
		h.setAgent(func(a *core.AgentState) { a.Stopped(core.StoppedByAgent, h.w.opts.Now()) })
	}
}

func (h *session) handle(sess agent.Session, ev agent.Event) {
	w := h.w
	now := w.opts.Now().UTC()
	switch ev := ev.(type) {
	case agent.Ready:
		h.setAgent(func(a *core.AgentState) { a.Session = ev.ID })
	case agent.TurnStart:
		h.record(h.snapshot(), stats.Event{At: now, Kind: "turn"})
		h.mu.Lock()
		h.working, h.turnAt = true, now
		h.mu.Unlock()
		h.setAgent(func(a *core.AgentState) { a.Working, a.TurnStarted, a.Error = true, now, "" })
	case agent.Message:
		kind := ev.Role
		if kind == "" {
			kind = core.ItemAssistant
		}
		id := ev.ID
		if id == "" {
			id = newID(6)
		}
		h.appendItem(core.Item{ID: id, Kind: kind, Text: ev.Text, Partial: ev.Partial, Agent: ev.Agent, Time: now})
	case agent.ToolCall:
		se := h.snapshot()
		h.record(se, stats.Event{At: now, Kind: "tool", ID: ev.ID, Name: ev.Tool, Input: ev.Input, Agent: ev.Agent, Model: se.Agent.Model,
			Effort: se.Agent.Effort, Cwd: agentDir(se)})
		h.mu.Lock()
		h.calls[ev.ID] = ev
		h.mu.Unlock()
		it := core.Item{ID: ev.ID, Kind: core.ItemTool, Tool: ev.Tool, Input: string(ev.Input), Title: toolTitle(ev.Tool, ev.Input),
			Agent: ev.Agent, Time: now}
		h.appendItem(it)
		h.setAgent(func(a *core.AgentState) { a.Stats.ToolCalls++ })
		if ev.Agent != "" {
			h.subagentTool(ev.Agent)
		}
	case agent.ToolResult:
		h.record(h.snapshot(), stats.Event{At: now, Kind: "result", ID: ev.ID, Error: ev.IsError})
		h.toolResult(ev, now)
	case agent.Permission:
		w.goSafe("a permission", func() { h.permission(sess, ev) })
	case agent.Question:
		w.goSafe("a question", func() { h.question(sess, ev) })
	case agent.Subagent:
		h.subagent(ev, now)
	case agent.TurnEnd:
		se := h.snapshot()
		u := ev.Usage
		h.record(se, stats.Event{At: now, Kind: "request", Model: se.Agent.Model, Effort: se.Agent.Effort, Window: int64(u.ContextMax),
			Tokens: stats.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite5m: u.CacheWrite}})
		h.turnEnd(ev, now)
	case agent.Failure:
		h.appendItem(core.Item{ID: newID(6), Kind: core.ItemError, Text: ev.Err, Time: now})
		h.setAgent(func(a *core.AgentState) { a.Error = ev.Err })
	case agent.UsageUpdate:
		w.usageSampled(ev.Usage)
		if acc := h.snapshot().Agent.Accounts; ev.Usage.Account != "" && (len(acc) == 0 || acc[len(acc)-1].Account != ev.Usage.Account) {
			h.setAgent(func(a *core.AgentState) { a.RanOn(now, ev.Usage.Account, ev.Usage.AccountName) })
		}
	case agent.AuthExpired:
		h.mu.Lock()
		h.relogin = h.relogin || h.working
		h.mu.Unlock()
		h.appendItem(core.Item{ID: newID(6), Kind: core.ItemError, Text: "Signed out: " + ev.Detail, Time: now})
		w.loginLost(h.id, ev)
	case agent.AuthRestored:
		if w.signedOut(ev.Provider) {
			w.goSafe("a login check", func() { w.checkLogin(ev.Provider, true) })
		}
	case agent.Exited:
		if ev.Err != "" {
			h.appendItem(core.Item{ID: newID(6), Kind: core.ItemSystem, Text: "The agent exited: " + ev.Err, Time: now})
		}
	}
}

// snapshot is the session's state now, or its zero value once it's gone.
func (h *session) snapshot() core.SessionState {
	se, _ := h.w.session(h.id)
	return se
}

func (h *session) appendItem(it core.Item) {
	h.mu.Lock()
	h.items[it.ID] = it
	h.mu.Unlock()
	_ = h.w.emit(core.TranscriptAppended{Session: h.id, Items: []core.Item{it}})
}

func (h *session) toolResult(ev agent.ToolResult, now time.Time) {
	h.mu.Lock()
	call := h.calls[ev.ID]
	it, ok := h.items[ev.ID]
	h.mu.Unlock()
	if !ok {
		it = core.Item{ID: ev.ID, Kind: core.ItemTool, Tool: call.Tool, Time: now}
	}
	it.Done, it.Output, it.IsError, it.Diff = true, clipOutput(ev.Output), ev.IsError, ev.Diff
	if !it.Time.IsZero() {
		it.Duration = now.Sub(it.Time).Milliseconds()
	}
	h.appendItem(it)
	if strings.HasSuffix(call.Tool, "test_run") {
		h.linkRun(ev)
	}
	act, ok := activityFor(call, ev)
	if !ok {
		return
	}
	act.Item = ev.ID
	if run, ok := testRunFrom(call, ev, now); ok {
		run.ID, run.By, run.Item = newID(4), "agent", ev.ID
		h.mu.Lock()
		if len(h.delivered) > 0 {
			run.Thread = h.delivered[0]
		}
		h.runs = append(h.runs, run.ID)
		h.mu.Unlock()
		_ = h.w.emit(core.TestRunRecorded{Session: h.id, Run: run})
		act.Run, act.Failed = run.ID, !run.OK
	}
	h.mu.Lock()
	h.activity = append(h.activity, act)
	threads := slices.Clone(h.delivered)
	h.mu.Unlock()
	if act.Kind == "edit" && act.Path != "" {
		for _, tid := range threads {
			_, _ = h.w.mutateThread(h.id, tid, func(t *core.Thread) error {
				t.Touched = touch(t.Touched, h.w.projectOf(h.id, act.Path), act.Path, act.Added, act.Removed)
				return nil
			})
		}
	}
}

// linkRun ties the run a test_run call recorded to the call's transcript item, so the console can link it.
func (h *session) linkRun(ev agent.ToolResult) {
	m := citesRun.FindStringSubmatch(ev.Output)
	if m == nil {
		return
	}
	se, err := h.w.session(h.id)
	if err != nil {
		return
	}
	if r, ok := se.Run(m[1]); ok && r.Item == "" {
		r.Item = ev.ID
		_ = h.w.emit(core.TestRunRecorded{Session: h.id, Run: r})
	}
}

// projectOf is the project a path belongs to, and the path relative to it.
func (w *Host) projectOf(id core.SessionID, path string) string {
	se, err := w.session(id)
	if err != nil {
		return ""
	}
	for _, p := range se.Projects {
		if rel, err := filepath.Rel(p.Dir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return p.Name
		}
	}
	return ""
}

func touch(ts []core.Touched, project, path string, added, removed int) []core.Touched {
	for i, t := range ts {
		if t.Path == path {
			ts[i].Added += added
			ts[i].Removed += removed
			return ts
		}
	}
	return append(ts, core.Touched{Project: project, Path: path, Added: added, Removed: removed})
}

func (h *session) subagent(ev agent.Subagent, now time.Time) {
	se, err := h.w.session(h.id)
	if err != nil {
		return
	}
	a := core.Subagent{ID: ev.ID, Description: ev.Description, Type: ev.Type, Prompt: ev.Prompt, Running: !ev.Done, Started: now}
	for _, old := range se.Subagents {
		if old.ID == ev.ID {
			a.Started, a.Tools, a.Threads = old.Started, old.Tools, old.Threads
			if a.Description == "" {
				a.Description, a.Type, a.Prompt = old.Description, old.Type, old.Prompt
			}
		}
	}
	if ev.Done {
		a.Ended, a.Result = now, ev.Result
	}
	if a.Threads == nil {
		h.mu.Lock()
		a.Threads = slices.Clone(h.delivered)
		h.mu.Unlock()
	}
	_ = h.w.emit(core.SubagentChanged{Session: h.id, Subagent: a})
	h.w.restatus(h.id)
}

func (h *session) subagentTool(id string) {
	se, err := h.w.session(h.id)
	if err != nil {
		return
	}
	for _, a := range se.Subagents {
		if a.ID == id {
			a.Tools++
			_ = h.w.emit(core.SubagentChanged{Session: h.id, Subagent: a})
		}
	}
}

func (h *session) turnEnd(ev agent.TurnEnd, now time.Time) {
	h.mu.Lock()
	h.working, h.interrupting = false, false
	started := h.turnAt
	delivered := append(h.held, h.delivered...)
	h.held, h.delivered = nil, nil
	if len(ev.Background) > 0 {
		h.held, delivered = delivered, nil
	}
	h.mu.Unlock()
	for _, tid := range delivered {
		_, _ = h.w.mutateThread(h.id, tid, func(t *core.Thread) error { t.Busy = false; return nil })
	}
	h.setAgent(func(a *core.AgentState) {
		a.Working = false
		a.Background = ev.Background
		a.Stats.Turns++
		u := ev.Usage
		a.Stats.Input += u.Input
		a.Stats.Output += u.Output
		a.Stats.CacheRead += u.CacheRead
		a.Stats.CacheWrite += u.CacheWrite
		a.Stats.Cost += u.Cost
		if u.Context > 0 {
			a.Stats.Context = u.Context
		}
		if u.ContextMax > 0 {
			a.Stats.ContextMax = u.ContextMax
		}
		if !started.IsZero() {
			a.Stats.Running += int64(now.Sub(started).Seconds())
		}
	})
	h.kick()
	if len(ev.Background) == 0 {
		h.idle()
	}
}

// askUser opens an ask and waits for the user's answer, or for ctx.
func (h *session) askUser(ctx context.Context, ask core.Ask) (AnswerReq, bool) {
	if ask.ID == "" {
		ask.ID = newID(6)
	}
	ask.Created = h.w.opts.Now().UTC()
	ch := make(chan AnswerReq, 1)
	h.mu.Lock()
	h.waiting[ask.ID] = ch
	h.mu.Unlock()
	if err := h.w.emit(core.AskOpened{Session: h.id, Ask: ask}); err != nil {
		return AnswerReq{}, false
	}
	h.w.restatus(h.id)
	defer h.w.restatus(h.id)
	select {
	case a, ok := <-ch:
		return a, ok
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.waiting, ask.ID)
		h.mu.Unlock()
		_ = h.w.emit(core.AskClosed{Session: h.id, ID: ask.ID, Decision: "gone"})
		return AnswerReq{}, false
	}
}

// answer is the user's answer to an ask.
func (h *session) answer(r AnswerReq) error {
	h.mu.Lock()
	ch, ok := h.waiting[r.Ask]
	delete(h.waiting, r.Ask)
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("nothing is asking %q", r.Ask)
	}
	ch <- r
	_ = h.w.emit(core.AskClosed{Session: h.id, ID: r.Ask, Decision: r.Decision, Note: r.Note})
	return nil
}

// permission is layer 1 asking: the session's rules and the app's policy hooks answer first (layer 2), and the
// user only when neither does (agents.md → Permissions).
func (h *session) permission(sess agent.Session, p agent.Permission) {
	ask := core.Ask{ID: p.ID, Kind: core.AskPermission, Tool: p.Tool, Summary: p.Summary, Input: string(p.Input), Rule: p.Rule}
	if ask.Rule == "" {
		ask.Rule = RuleFor(p.Tool, p.Input)
	}
	if postsReview(p.Tool, p.Input) {
		_ = sess.Answer(h.ctx, p.ID, agent.Decision{Note: NoPosting})
		h.appendItem(core.Item{ID: newID(6), Kind: core.ItemSystem, Time: h.w.opts.Now().UTC(), Text: p.Tool + " denied: agents never post to GitHub reviews"})
		return
	}
	if v, ok := h.w.policy(h.id, ask); ok {
		_ = sess.Answer(h.ctx, p.ID, agent.Decision{Allow: v.Decision == "allow", Note: v.Note})
		h.appendItem(core.Item{ID: newID(6), Kind: core.ItemSystem, Time: h.w.opts.Now().UTC(),
			Text: fmt.Sprintf("%s %s by %s", p.Tool, map[bool]string{true: "allowed", false: "denied"}[v.Decision == "allow"], v.By)})
		return
	}
	a, ok := h.askUser(h.ctx, ask)
	if !ok {
		return
	}
	d := agent.Decision{Allow: a.Decision == "allow" || a.Decision == "always", Always: a.Decision == "always", Note: a.Note}
	if !d.Allow && d.Note == "" {
		d.Note = "Denied by the user in Oriel."
	} else if !d.Allow {
		d.Note = "Denied by the user in Oriel: " + d.Note
	}
	if d.Always {
		_ = h.w.addRule(h.id, ask.Rule)
	}
	_ = sess.Answer(h.ctx, p.ID, d)
}

func (h *session) question(sess agent.Session, q agent.Question) {
	a, ok := h.askUser(h.ctx, core.Ask{ID: q.ID, Kind: core.AskQuestion, Summary: questionSummary(q.Questions), Questions: q.Questions})
	if !ok {
		return
	}
	_ = sess.Answer(h.ctx, q.ID, agent.Decision{Allow: true, Answers: a.Answers, Note: a.Note})
}

func questionSummary(qs []core.Question) string {
	if len(qs) == 0 {
		return "The agent has a question"
	}
	return qs[0].Question
}

// approve is layer 3 asking: the sandbox holds a connection or a gateway call while the user decides.
func (h *session) approve(ctx context.Context, ask core.Ask) (Approval, error) {
	if v, ok := h.w.policy(h.id, ask); ok {
		return Approval{Allow: v.Decision == "allow", By: v.By, Note: v.Note}, nil
	}
	a, ok := h.askUser(ctx, ask)
	if !ok {
		return Approval{}, ctx.Err()
	}
	if a.Decision == "always" && a.Scope == "" {
		_ = h.w.addRule(h.id, ask.Rule)
	}
	return Approval{Allow: a.Decision == "allow" || a.Decision == "always", Scope: a.Scope, By: "the user", Note: a.Note}, nil
}

func clipOutput(s string) string {
	const max = 8000
	if len(s) <= max {
		return s
	}
	return s[:max/2] + "\n…\n" + s[len(s)-max/2:]
}

// toolTitle is a tool call in one line, as the console shows it: "Read internal/ratelimit/limiter.go".
func toolTitle(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	name := tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		name = tool[i+2:]
	}
	switch tool {
	case "Bash", "commandExecution", "shell":
		if c := str("command"); c != "" {
			return "Bash " + firstLine(c)
		}
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		return tool + " " + str("file_path")
	case "Grep":
		return fmt.Sprintf("Search %q", str("pattern"))
	case "Glob":
		return "Find " + str("pattern")
	case "Task", "Agent":
		return "Agent " + str("description")
	case "WebFetch":
		return "Fetch " + str("url")
	case "fileChange":
		return "Edit " + str("path")
	}
	return name
}
