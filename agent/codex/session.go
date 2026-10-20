package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/core"
)

func (p *Provider) Start(ctx context.Context, s agent.StartSpec) (agent.Session, error) {
	return p.start(ctx, s, "")
}

func (p *Provider) Resume(ctx context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	if id == "" {
		return nil, errors.New("codex: no thread id to resume")
	}
	return p.start(ctx, s, id)
}

// gate is the approval policy and sandbox mode for a policy. In a sandbox there's no approval: the sandbox and its
// gateway are the gate.
func gate(s agent.StartSpec) (approval, sandbox string, err error) {
	if s.Sandbox != nil {
		switch s.Policy {
		case agent.PolicyAsk, agent.PolicyLight, agent.PolicyBypass, "":
			return "never", "danger-full-access", nil
		}
	}
	switch s.Policy {
	case agent.PolicyAsk, "":
		return "untrusted", "workspace-write", nil
	case agent.PolicyLight:
		return "on-request", "workspace-write", nil
	case agent.PolicyBypass:
		if s.Sandbox == nil {
			return "", "", errors.New("codex: bypass is only for a sandboxed session: outside one, nothing would gate the agent")
		}
		return "never", "danger-full-access", nil
	}
	return "", "", fmt.Errorf("codex: unknown policy %q", s.Policy)
}

type session struct {
	p     *Provider
	spec  agent.StartSpec
	proc  agent.Process
	rpc   *jsonrpc.Conn
	tools *toolServer
	errs  *tail

	events   chan agent.Event
	emu      sync.RWMutex
	closed   bool
	closing  chan struct{}
	finished chan struct{}
	once     sync.Once

	mu       sync.Mutex
	id       string
	turn     string
	history  bool // the thread has a turn to fork from
	preamble string
	held     string // a denial's note that missed its turn, said with the next prompt
	failed   bool   // this turn has reported its Failure
	base     tokens // the thread's total when this turn started
	total    tokens
	last     tokens
	window   int64
	partial  map[string]string
	started  map[string]bool
	changes  map[string][]fileChange
	pending  map[string]func(agent.Decision) error
	forks    map[string]*forkTurn
	limits   rateLimits
	acct     account // who's signed in: the account the plan's windows belong to
	nextAsk  int
}

type tokens struct {
	TotalTokens           int64 `json:"totalTokens"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
}

type forkTurn struct {
	said []string
	err  string
	done chan struct{}
}

func (p *Provider) start(ctx context.Context, spec agent.StartSpec, resume string) (agent.Session, error) {
	approval, sandbox, err := gate(spec)
	if err != nil {
		return nil, err
	}
	tools, err := serveTools(spec.Tools)
	if err != nil {
		return nil, fmt.Errorf("codex: serving Oriel's tools: %w", err)
	}
	cmd := exec.Command(p.bin(), "app-server")
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), spec.Env...)
	errs := &tail{}
	cmd.Stderr = errs
	ex := spec.Exec
	if ex == nil {
		ex = hostexec.Exec{}
	}
	proc, err := ex.Start(ctx, cmd)
	if err != nil {
		tools.Close()
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	s := &session{
		p: p, spec: spec, proc: proc, tools: tools, errs: errs,
		events: make(chan agent.Event, 256), closing: make(chan struct{}), finished: make(chan struct{}),
		preamble: spec.Pack.Text,
		partial:  map[string]string{}, started: map[string]bool{}, changes: map[string][]fileChange{},
		pending: map[string]func(agent.Decision) error{}, forks: map[string]*forkTurn{},
	}
	s.rpc = jsonrpc.NewConn(proc.Stdout(), proc.Stdin(), jsonrpc.Handlers{Request: s.onRequest, Notify: s.onNotify}, jsonrpc.Options{})
	go s.wait()
	fail := func(err error) (agent.Session, error) {
		s.Close()
		if t := errs.String(); t != "" {
			err = fmt.Errorf("%w\n%s", err, t)
		}
		return nil, err
	}
	if err := initialize(ctx, s.rpc); err != nil {
		return fail(err)
	}
	acct, err := readAccount(ctx, s.rpc)
	if err != nil {
		return fail(err)
	}
	if err := requireChatGPT(acct, spec.Env); err != nil {
		return fail(err)
	}
	s.acct = acct
	cfg, err := s.config()
	if err != nil {
		return fail(err)
	}
	params := map[string]any{"approvalPolicy": approval, "sandbox": sandbox, "config": cfg}
	if spec.Cwd != "" {
		params["cwd"] = spec.Cwd
	}
	if spec.Model != "" {
		params["model"] = spec.Model
	}
	method := "thread/start"
	if resume != "" {
		method = "thread/resume"
		params["threadId"] = resume
	}
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.rpc.Call(ctx, method, params, &r); err != nil {
		return fail(fmt.Errorf("codex %s: %w", method, err))
	}
	if r.Thread.ID == "" {
		return fail(fmt.Errorf("codex %s: no thread id", method))
	}
	s.mu.Lock()
	s.id = r.Thread.ID
	s.history = resume != ""
	s.mu.Unlock()
	s.emit(agent.Ready{ID: r.Thread.ID})
	if spec.Prompt != "" {
		if err := s.Send(ctx, agent.Input{Text: spec.Prompt}); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

// config is the thread's config overrides: Oriel's tools as the MCP server oriel, and the sandbox's gateway.
func (s *session) config() (map[string]any, error) {
	cfg := map[string]any{
		"mcp_servers.oriel": map[string]any{
			"command":                     s.p.orielBin(),
			"args":                        []string{"hook", "mcp"},
			"env":                         map[string]string{SockEnv: s.tools.path},
			"default_tools_approval_mode": "approve",
		},
	}
	if s.spec.Sandbox == nil || s.spec.Sandbox.GatewayMCP == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(s.spec.Sandbox.GatewayMCP)
	if err != nil {
		return nil, fmt.Errorf("codex: the gateway's MCP config: %w", err)
	}
	var f struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("codex: the gateway's MCP config: %w", err)
	}
	for name, m := range f.MCPServers {
		c := map[string]any{}
		if m.URL != "" {
			c["url"] = m.URL
			if len(m.Headers) > 0 {
				c["http_headers"] = m.Headers
			}
		} else {
			c["command"] = m.Command
			if len(m.Args) > 0 {
				c["args"] = m.Args
			}
			if len(m.Env) > 0 {
				c["env"] = m.Env
			}
		}
		cfg["mcp_servers."+name] = c
	}
	return cfg, nil
}

// wait reports the process gone, once everything it said has been passed on.
func (s *session) wait() {
	err := s.proc.Wait()
	select {
	case <-s.rpc.Done():
	case <-time.After(2 * time.Second):
	}
	s.rpc.Close()
	s.tools.Close()
	ex := agent.Exited{}
	select {
	case <-s.closing:
	default:
		if err != nil {
			ex.Err = err.Error()
			if t := s.errs.String(); t != "" {
				ex.Err += ": " + t
			}
		}
	}
	s.emit(ex)
	s.emu.Lock()
	s.closed = true
	close(s.events)
	s.emu.Unlock()
	close(s.finished)
}

func (s *session) emit(ev agent.Event) {
	s.emu.RLock()
	defer s.emu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.events <- ev:
	case <-s.closing:
	}
}

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Events() <-chan agent.Event { return s.events }

func (s *session) Terminal() agent.Terminal { return nil }

func (s *session) Close() error {
	s.once.Do(func() {
		close(s.closing)
		s.rpc.Close()
		s.proc.Kill()
	})
	<-s.finished
	return nil
}

func (s *session) Send(ctx context.Context, in agent.Input) error {
	s.mu.Lock()
	id, running := s.id, s.turn
	text := in.Text
	if s.held != "" {
		text = s.held + "\n\n" + text
		s.held = ""
	}
	if s.preamble != "" {
		text = s.preamble + "\n\n" + text
		s.preamble = ""
	}
	s.mu.Unlock()
	input := []map[string]any{{"type": "text", "text": text}}
	for _, img := range in.Images {
		input = append(input, map[string]any{"type": "localImage", "path": img})
	}
	if in.Urgent && running != "" {
		return s.rpc.Call(ctx, "turn/steer", map[string]any{"threadId": id, "input": input, "expectedTurnId": running}, nil)
	}
	params := map[string]any{"threadId": id, "input": input}
	if s.spec.Effort != "" {
		params["effort"] = s.spec.Effort
	}
	var r struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := s.rpc.Call(ctx, "turn/start", params, &r); err != nil {
		return fmt.Errorf("codex turn/start: %w", err)
	}
	return nil
}

func (s *session) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	id, turn := s.id, s.turn
	s.mu.Unlock()
	if turn == "" {
		return nil
	}
	return s.rpc.Call(ctx, "turn/interrupt", map[string]any{"threadId": id, "turnId": turn}, nil)
}

// forkInstructions keep a side question to talking.
const forkInstructions = "This is a side question about the conversation so far. Answer it from what you already " +
	"know, briefly, without running commands or changing files."

// Fork answers a side question on an ephemeral fork of the thread, which nothing is saved to.
func (s *session) Fork(ctx context.Context, f agent.ForkSpec) (string, error) {
	s.mu.Lock()
	id, ok := s.id, s.history
	s.mu.Unlock()
	if !ok {
		return "", agent.ErrNoFork
	}
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	err := s.rpc.Call(ctx, "thread/fork", map[string]any{
		"threadId": id, "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only",
		"developerInstructions": forkInstructions,
	}, &r)
	if err != nil {
		return "", fmt.Errorf("codex thread/fork: %w", err)
	}
	ft := &forkTurn{done: make(chan struct{})}
	s.mu.Lock()
	s.forks[r.Thread.ID] = ft
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.forks, r.Thread.ID)
		s.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.rpc.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": r.Thread.ID}, nil)
		}()
	}()
	input := []map[string]any{{"type": "text", "text": f.Prompt}}
	if err := s.rpc.Call(ctx, "turn/start", map[string]any{"threadId": r.Thread.ID, "input": input}, nil); err != nil {
		return "", fmt.Errorf("codex turn/start: %w", err)
	}
	select {
	case <-ft.done:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.closing:
		return "", errors.New("codex: the session closed")
	}
	if ft.err != "" {
		return "", errors.New(ft.err)
	}
	return strings.Join(ft.said, "\n"), nil
}

func (s *session) Answer(ctx context.Context, id string, d agent.Decision) error {
	s.mu.Lock()
	answer := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if answer == nil {
		return fmt.Errorf("codex: nothing is waiting on %q", id)
	}
	return answer(d)
}

// ask records what answers a request, and returns its id.
func (s *session) ask(answer func(agent.Decision) error) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAsk++
	id := fmt.Sprintf("ask-%d", s.nextAsk)
	s.pending[id] = answer
	return id
}

// tell sends a denial's note to the turn that was denied, or holds it for the next prompt.
func (s *session) tell(threadID, turnID, note string) {
	text := "Denied by the user: " + note
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := s.rpc.Call(ctx, "turn/steer", map[string]any{
		"threadId": threadID, "expectedTurnId": turnID, "input": []map[string]any{{"type": "text", "text": text}},
	}, nil)
	if err != nil {
		s.mu.Lock()
		s.held = text
		s.mu.Unlock()
	}
}

func (s *session) onRequest(r *jsonrpc.Request) {
	var p struct {
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
		ItemID    string `json:"itemId"`
		Command   string `json:"command"`
		Cwd       string `json:"cwd"`
		Reason    string `json:"reason"`
		GrantRoot string `json:"grantRoot"`
		Questions []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
		ServerName string `json:"serverName"`
		Message    string `json:"message"`
	}
	json.Unmarshal(r.Params, &p)
	main := p.ThreadID == s.ID()
	switch r.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		if !main {
			r.Reply(map[string]any{"decision": "decline"}, nil)
			return
		}
		perm := agent.Permission{Tool: "Bash", Summary: p.Command, Rule: "`" + p.Command + "` for the rest of this session"}
		in := map[string]any{"command": p.Command, "cwd": p.Cwd}
		if r.Method == "item/fileChange/requestApproval" {
			s.mu.Lock()
			var paths []string
			for _, c := range s.changes[p.ItemID] {
				paths = append(paths, c.Path)
			}
			s.mu.Unlock()
			perm.Tool, perm.Summary = "Edit", strings.Join(paths, ", ")
			perm.Rule = "edits to " + perm.Summary + " for the rest of this session"
			in = map[string]any{"files": paths}
			if p.GrantRoot != "" {
				in["grantRoot"] = p.GrantRoot
				perm.Rule = "writes under " + p.GrantRoot + " for the rest of this session"
				if perm.Summary == "" {
					perm.Summary = "write under " + p.GrantRoot
				}
			}
		}
		if p.Reason != "" {
			in["reason"] = p.Reason
		}
		perm.Input = jsonOf(in)
		perm.ID = s.ask(func(d agent.Decision) error {
			decision := "decline"
			switch {
			case d.Allow && d.Always:
				decision = "acceptForSession"
			case d.Allow:
				decision = "accept"
			}
			if err := r.Reply(map[string]any{"decision": decision}, nil); err != nil {
				return err
			}
			if !d.Allow && d.Note != "" {
				s.tell(p.ThreadID, p.TurnID, d.Note)
			}
			return nil
		})
		s.emit(perm)
	case "item/tool/requestUserInput":
		if !main {
			r.Reply(map[string]any{"answers": map[string]any{}}, nil)
			return
		}
		q := agent.Question{}
		for _, pq := range p.Questions {
			cq := core.Question{Header: pq.Header, Question: pq.Question}
			for _, o := range pq.Options {
				cq.Options = append(cq.Options, o.Label)
			}
			q.Questions = append(q.Questions, cq)
		}
		qs := p.Questions
		q.ID = s.ask(func(d agent.Decision) error {
			answers := map[string]any{}
			for _, pq := range qs {
				for _, key := range []string{pq.Question, pq.ID, pq.Header} {
					if a, ok := d.Answers[key]; ok && key != "" {
						answers[pq.ID] = map[string]any{"answers": []string{a}}
						break
					}
				}
			}
			return r.Reply(map[string]any{"answers": answers}, nil)
		})
		s.emit(q)
	case "mcpServer/elicitation/request":
		if p.ServerName == "oriel" {
			r.Reply(map[string]any{"action": "accept", "content": map[string]any{}}, nil)
			return
		}
		if !main {
			r.Reply(map[string]any{"action": "decline"}, nil)
			return
		}
		perm := agent.Permission{Tool: "mcp__" + p.ServerName, Summary: p.Message, Input: r.Params,
			Rule: "this request from " + p.ServerName}
		perm.ID = s.ask(func(d agent.Decision) error {
			if d.Allow {
				return r.Reply(map[string]any{"action": "accept", "content": map[string]any{}}, nil)
			}
			return r.Reply(map[string]any{"action": "decline"}, nil)
		})
		s.emit(perm)
	default:
		r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "oriel doesn't answer " + r.Method})
	}
}
