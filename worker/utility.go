package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// Utility agents (agents.md, "Nothing else may call claude -p either"): an agent with no session, started through
// its provider like any other (Claude in its PTY with the mod), in a folder of Oriel's, on a small model, offered
// only the app's tools for it. It reads; every permission it asks for is refused but the provider tools it's
// allowed, and nobody answers its questions. The caller holds a *Utility while it wants it: the agent stops at
// its Close.

// UtilitySpec is a utility agent to start.
type UtilitySpec struct {
	Name     string `json:"name"`               // what it's for ("assistant"): its folder, and who the app's tools are for
	Provider string `json:"provider,omitempty"` // "" for the first installed and signed in
	Model    string `json:"model,omitempty"`    // "" for the provider's small model, else its default
	Pack     string `json:"pack"`               // what it's told: a Claude skill, Codex's first message
	Prompt   string `json:"prompt"`             // the first turn
	// Allow are the provider's own tools it may use without asking: WebSearch, WebFetch.
	Allow []string `json:"allow,omitempty"`
}

// UtilityEvent is what a utility agent did.
type UtilityEvent struct {
	Type     string          `json:"type"`               // ready, turn-start, message, tool-call, tool-result, turn-end, failure, exited
	ID       string          `json:"id,omitempty"`       // ready: the agent's, for send; a tool call's
	Provider string          `json:"provider,omitempty"` // ready
	Model    string          `json:"model,omitempty"`    // ready
	Text     string          `json:"text,omitempty"`     // a message, a failure, a tool's result
	Tool     string          `json:"tool,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Error    bool            `json:"error,omitempty"` // a tool result that failed
}

// UtilityReq is a turn for a utility agent, or (with no Text) the one to interrupt.
type UtilityReq struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

var utilityName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// utilityRefusal is what an agent is told of a permission it isn't given.
const utilityRefusal = "This agent only reads: it can't run commands or change files. Use your tools, or say what the user should do."

type utilities struct {
	mu   sync.Mutex
	runs map[string]agent.Session
}

func (u *utilities) get(id string) (agent.Session, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if s, ok := u.runs[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("no utility agent %q: it has stopped", id)
}

// runUtility starts a utility agent and sends its events until it exits, ctx ends or the host closes.
func (w *Worker) runUtility(ctx context.Context, spec UtilitySpec, send func(UtilityEvent) error) error {
	if !utilityName.MatchString(spec.Name) {
		return fmt.Errorf("%q is not a utility agent's name: lower-case letters, digits, - and _", spec.Name)
	}
	p, model, err := w.utilityProvider(ctx, spec)
	if err != nil {
		return err
	}
	id := newID(4)
	dir := filepath.Join(w.opts.Home, "utility", spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sess, err := p.Start(ctx, agent.StartSpec{Cwd: dir, Model: model, Policy: agent.PolicyAsk, Exec: w.opts.Exec,
		Pack:     agent.Pack{Name: spec.Name, Skill: spec.Name, Text: spec.Pack},
		Prompt:   spec.Prompt,
		Tools:    w.appTools(ToolsReq{Agent: spec.Name}),
		StateDir: filepath.Join(dir, "runs", id),
		Env:      []string{"ORIEL_UTILITY=" + spec.Name}})
	if err != nil {
		return err
	}
	defer sess.Close()
	w.utility.mu.Lock()
	if w.utility.runs == nil {
		w.utility.runs = map[string]agent.Session{}
	}
	w.utility.runs[id] = sess
	w.utility.mu.Unlock()
	defer func() {
		w.utility.mu.Lock()
		delete(w.utility.runs, id)
		w.utility.mu.Unlock()
	}()
	if err := send(UtilityEvent{Type: "ready", ID: id, Provider: p.ID(), Model: model}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.closed:
			return nil
		case ev, ok := <-sess.Events():
			if !ok {
				return send(UtilityEvent{Type: "exited"})
			}
			out, end := utilityEvent(ctx, sess, spec, ev)
			if out.Type != "" {
				if err := send(out); err != nil {
					return err
				}
			}
			if end {
				return nil
			}
		}
	}
}

// utilityEvent is what an agent's event says to the caller, answering its asks on the way; end says it's over.
func utilityEvent(ctx context.Context, sess agent.Session, spec UtilitySpec, ev agent.Event) (UtilityEvent, bool) {
	switch ev := ev.(type) {
	case agent.TurnStart:
		return UtilityEvent{Type: "turn-start"}, false
	case agent.Message:
		if ev.Partial || ev.Agent != "" || ev.Role != "" && ev.Role != core.ItemAssistant || strings.TrimSpace(ev.Text) == "" {
			return UtilityEvent{}, false
		}
		return UtilityEvent{Type: "message", Text: ev.Text}, false
	case agent.ToolCall:
		if ev.Agent != "" {
			return UtilityEvent{}, false
		}
		return UtilityEvent{Type: "tool-call", ID: ev.ID, Tool: toolName(ev.Tool), Input: ev.Input}, false
	case agent.ToolResult:
		return UtilityEvent{Type: "tool-result", ID: ev.ID, Text: ev.Output, Error: ev.IsError}, false
	case agent.Permission:
		d := agent.Decision{Note: utilityRefusal}
		if slices.Contains(spec.Allow, ev.Tool) {
			d = agent.Decision{Allow: true}
		}
		_ = sess.Answer(ctx, ev.ID, d)
	case agent.Question:
		_ = sess.Answer(ctx, ev.ID, agent.Decision{Note: "Nobody answers questions here: ask in your answer instead, and end the turn."})
	case agent.TurnEnd:
		return UtilityEvent{Type: "turn-end"}, false
	case agent.Failure:
		return UtilityEvent{Type: "failure", Text: ev.Err}, false
	case agent.AuthExpired:
		return UtilityEvent{Type: "failure", Text: ev.Error()}, true
	case agent.Exited:
		return UtilityEvent{Type: "exited", Text: ev.Err}, true
	}
	return UtilityEvent{}, false
}

// toolName is a tool as Oriel knows it: mcp__oriel__sessions is sessions.
func toolName(tool string) string { return strings.TrimPrefix(tool, "mcp__oriel__") }

// utilityProvider is the provider and model a utility agent runs on.
func (w *Worker) utilityProvider(ctx context.Context, spec UtilitySpec) (agent.Provider, string, error) {
	var p agent.Provider
	if spec.Provider != "" {
		var ok bool
		if p, ok = w.opts.Providers.Get(spec.Provider); !ok {
			return nil, "", fmt.Errorf("no agent provider %q", spec.Provider)
		}
		if w.signedOut(p.ID()) {
			return nil, "", fmt.Errorf("%s is signed out on this machine", p.ID())
		}
	} else {
		for _, c := range w.opts.Providers.List() {
			if w.installed(ctx, c) {
				p = c
				break
			}
		}
		if p == nil {
			return nil, "", errors.New("no agent provider is installed and signed in")
		}
	}
	if spec.Model != "" {
		return p, spec.Model, nil
	}
	ms, _ := p.Models(ctx)
	for _, m := range ms {
		if m.Small {
			return p, m.ID, nil
		}
	}
	return p, defaultModel(ctx, p), nil
}

// sendUtility gives a utility agent a turn.
func (w *Worker) sendUtility(ctx context.Context, r UtilityReq) error {
	s, err := w.utility.get(r.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Text) == "" {
		return errors.New("nothing to say")
	}
	return s.Send(ctx, agent.Input{Text: r.Text})
}

// interruptUtility stops a utility agent's turn.
func (w *Worker) interruptUtility(ctx context.Context, r UtilityReq) error {
	s, err := w.utility.get(r.ID)
	if err != nil {
		return err
	}
	return s.Interrupt(ctx)
}

// Utility is a utility agent as the app holds it.
type Utility struct {
	w      *Worker
	cancel context.CancelFunc
	ready  UtilityEvent
	events chan UtilityEvent
}

// StartUtility starts a utility agent; it runs until Close.
func (w *Worker) StartUtility(ctx context.Context, spec UtilitySpec) (_ *Utility, err error) {
	defer w.recover("StartUtility", &err)
	run, cancel := context.WithCancel(context.Background())
	events := make(chan UtilityEvent, 64)
	started := make(chan error, 1)
	w.goSafe("a utility agent", func() {
		defer close(events)
		first := true
		err := w.runUtility(run, spec, func(ev UtilityEvent) error {
			if first {
				first = false
				events <- ev
				started <- nil
				return nil
			}
			select {
			case events <- ev:
				return nil
			case <-run.Done():
				return run.Err()
			}
		})
		if first {
			started <- cmpErr(err, errors.New("the utility agent ended before it started"))
		}
	})
	select {
	case err := <-started:
		if err != nil {
			cancel()
			return nil, err
		}
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	return &Utility{w: w, cancel: cancel, ready: <-events, events: events}, nil
}

func cmpErr(err, or error) error {
	if err != nil {
		return err
	}
	return or
}

// Ready is what the agent said as it started: its id, provider and model.
func (u *Utility) Ready() UtilityEvent { return u.ready }

// Events are what it does, until it's gone.
func (u *Utility) Events() <-chan UtilityEvent { return u.events }

func (u *Utility) Send(ctx context.Context, text string) (err error) {
	defer u.w.recover("Utility.Send", &err)
	return u.w.sendUtility(ctx, UtilityReq{ID: u.ready.ID, Text: text})
}

func (u *Utility) Interrupt(ctx context.Context) (err error) {
	defer u.w.recover("Utility.Interrupt", &err)
	return u.w.interruptUtility(ctx, UtilityReq{ID: u.ready.ID})
}

// Close stops the agent.
func (u *Utility) Close() error {
	u.cancel()
	return nil
}
