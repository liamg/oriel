package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/eventq"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/jsonrpc"
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
// gateway are the gate. Outside one every command asks; under PolicyLight the session accepts the edits itself.
func gate(s agent.StartSpec) (approval, sandbox string, err error) {
	if s.Sandbox != nil {
		switch s.Policy {
		case agent.PolicyAsk, agent.PolicyLight, agent.PolicyBypass, agent.PolicyAuto, "":
			return "never", "danger-full-access", nil
		}
	}
	switch s.Policy {
	case agent.PolicyAsk, agent.PolicyLight, agent.PolicyAuto, "":
		return "untrusted", "workspace-write", nil
	case agent.PolicyBypass:
		return "", "", errors.New("codex: bypass is only for a sandboxed session: outside one, nothing would gate the agent")
	}
	return "", "", fmt.Errorf("codex: unknown policy %q", s.Policy)
}

type session struct {
	p      *Provider
	spec   agent.StartSpec
	prices price.Books
	proc   agent.Process
	rpc    *jsonrpc.Conn
	tools  *toolServer
	errs   *tail
	events *eventq.Queue

	closing  chan struct{}
	finished chan struct{}
	once     sync.Once

	mu       sync.Mutex
	id       string
	model    string // what the thread runs on, as codex said
	turn     string
	ended    string // the last turn that completed
	history  bool   // the thread has a turn to fork from
	held     string // a note that missed its turn, said with the next prompt
	failed   bool   // this turn has reported its Failure
	out      bool   // its login was said to be gone, and hasn't been seen back
	base     tokens // the thread's total when this turn started
	total    tokens
	last     tokens
	window   int64
	partial  map[string]string
	started  map[string]bool
	changes  map[string][]fileChange
	calls    map[string]item // MCP tool calls under way, by item
	children map[string]bool // the threads of the subagents it spawned
	user     []string        // the MCP servers codex's own config has
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

func newSession(p *Provider, spec agent.StartSpec) *session {
	return &session{p: p, spec: spec, prices: price.Or(spec.Prices), events: eventq.New(), closing: make(chan struct{}), finished: make(chan struct{}),
		partial: map[string]string{}, started: map[string]bool{}, changes: map[string][]fileChange{},
		calls: map[string]item{}, children: map[string]bool{},
		pending: map[string]func(agent.Decision) error{}, forks: map[string]*forkTurn{}}
}

func (p *Provider) start(ctx context.Context, spec agent.StartSpec, resume string) (agent.Session, error) {
	approval, sandbox, err := gate(spec)
	if err != nil {
		return nil, err
	}
	ex := spec.Exec
	bin := p.bin()
	if ex == nil {
		ex = hostexec.Exec{}
		if bin, err = exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("codex isn't installed: %w", err)
		}
	}
	tools, err := serveTools(spec.Offered)
	if err != nil {
		return nil, fmt.Errorf("codex: serving Oriel's tools: %w", err)
	}
	cmd := exec.Command(bin, "app-server")
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), spec.Env...)
	if spec.Sandbox != nil {
		cmd.Env = append(cmd.Env, agent.HookSocketEnv+"="+tools.path)
	}
	errs := &tail{}
	cmd.Stderr = errs
	proc, err := ex.Start(ctx, cmd)
	if err != nil {
		tools.Close()
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	s := newSession(p, spec)
	s.proc, s.tools, s.errs = proc, tools, errs
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
	s.mu.Lock()
	s.acct = acct
	s.mu.Unlock()
	user, err := s.userServers(ctx)
	if err != nil && s.strict() {
		return fail(err)
	}
	s.mu.Lock()
	s.user = user
	s.mu.Unlock()
	cfg, err := s.config(user)
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
	} else if in := instructions(spec); in != "" {
		params["developerInstructions"] = in
	}
	var r struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err := s.rpc.Call(ctx, method, params, &r); err != nil {
		return fail(fmt.Errorf("codex %s: %w", method, err))
	}
	if r.Thread.ID == "" {
		return fail(fmt.Errorf("codex %s: no thread id", method))
	}
	s.mu.Lock()
	s.id = r.Thread.ID
	s.model = r.Model
	if s.model == "" {
		s.model = spec.Model
	}
	s.history = resume != ""
	s.mu.Unlock()
	s.emit(agent.Ready{ID: r.Thread.ID, Model: r.Model})
	if spec.Prompt != "" {
		if err := s.Send(ctx, agent.Input{Text: spec.Prompt}); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

// instructions are what a new thread is told before anything is said, as its developer instructions: the pack's
// body, and where its memory is. Codex keeps them with the thread, so a resumed one has them already.
func instructions(spec agent.StartSpec) string {
	var parts []string
	if _, body, err := packs.Parse(spec.Pack.Text); err == nil && strings.TrimSpace(body) != "" {
		parts = append(parts, strings.TrimSpace(body))
	}
	if spec.Memory != "" {
		m := "## Memory\n\nYour memory directory is " + spec.Memory + ": keep what's worth remembering across " +
			"sessions there, a file for each memory, with an index of them in " + filepath.Join(spec.Memory, "MEMORY.md") + "."
		if b, err := os.ReadFile(filepath.Join(spec.Memory, "MEMORY.md")); err == nil && strings.TrimSpace(string(b)) != "" {
			m += " The index as it is now:\n\n" + strings.TrimSpace(string(b))
		}
		parts = append(parts, m)
	}
	return strings.Join(parts, "\n\n")
}

// strict says the user's own MCP servers are left out: the gateway stands in for them, or none are wanted.
func (s *session) strict() bool {
	return s.spec.NoMCP || s.spec.Sandbox != nil && s.spec.Sandbox.GatewayMCP != ""
}

// userServers are the MCP servers codex's own config gives a thread in the session's directory.
func (s *session) userServers(ctx context.Context) ([]string, error) {
	params := map[string]any{}
	if s.spec.Cwd != "" {
		params["cwd"] = s.spec.Cwd
	}
	var r struct {
		Config struct {
			MCPServers map[string]json.RawMessage `json:"mcp_servers"`
		} `json:"config"`
	}
	if err := s.rpc.Call(ctx, "config/read", params, &r); err != nil {
		return nil, fmt.Errorf("codex config/read: %w", err)
	}
	var out []string
	for name := range r.Config.MCPServers {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// config is the thread's config overrides: Oriel's tools as the MCP server oriel (in a sandbox, the oriel on its PATH
// at the socket's path there), the sandbox's gateway, web search off with NoWeb, and with strict the user's own
// servers (user) and ChatGPT's apps turned off.
func (s *session) config(user []string) (map[string]any, error) {
	bin, sock := s.p.orielBin(), s.tools.path
	if s.spec.Sandbox != nil {
		bin, sock = "oriel", s.spec.Sandbox.HookSocket
	}
	cfg := map[string]any{
		"mcp_servers.oriel": map[string]any{
			"command":                     bin,
			"args":                        []string{"hook", "mcp"},
			"env":                         map[string]string{SockEnv: sock},
			"default_tools_approval_mode": "approve",
		},
	}
	if s.spec.NoWeb {
		cfg["web_search"] = "disabled"
	}
	if s.strict() {
		cfg["features.apps"] = false
		for _, name := range user {
			if name != "oriel" {
				cfg["mcp_servers."+name+".enabled"] = false
			}
		}
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
		delete(cfg, "mcp_servers."+name+".enabled")
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
	s.events.Close()
	close(s.finished)
}

func (s *session) emit(ev agent.Event) { s.events.Push(ev) }

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Events() <-chan agent.Event { return s.events.Out() }

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
	s.mu.Lock()
	if s.turn == "" && r.Turn.ID != "" && r.Turn.ID != s.ended {
		s.turn = r.Turn.ID
	}
	s.mu.Unlock()
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

// forkOff are the features that give a thread tools (codex-rs/core/config.schema.json), all off for a fork.
var forkOff = []string{"shell_tool", "unified_exec", "view_image", "image_generation", "multi_agent", "collab", "apps",
	"memory_tool", "js_repl", "browser_use", "computer_use", "web_search", "web_search_request", "tool_search",
	"request_permissions_tool", "goals", "plugins"}

// forkConfig is a fork's config overrides: no web search, none of forkOff, and every MCP server off, Oriel's too.
func (s *session) forkConfig() (map[string]any, error) {
	s.mu.Lock()
	user := s.user
	s.mu.Unlock()
	base, err := s.config(user)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{"web_search": "disabled"}
	for _, f := range forkOff {
		cfg["features."+f] = false
	}
	for k, v := range base {
		m, ok := v.(map[string]any)
		if !ok || !strings.HasPrefix(k, "mcp_servers.") {
			continue
		}
		off := map[string]any{"enabled": false}
		for mk, mv := range m {
			off[mk] = mv
		}
		off["enabled"] = false
		cfg[k] = off
	}
	for _, name := range user {
		if _, ok := cfg["mcp_servers."+name]; !ok {
			cfg["mcp_servers."+name+".enabled"] = false
		}
	}
	return cfg, nil
}

// forkInstructions keep a side question to talking.
const forkInstructions = "This is a side question about the conversation so far. Answer it from what you already " +
	"know, briefly, without running commands or changing files."

// Fork answers a side question on an ephemeral fork of the thread, which nothing is saved to: read-only, asking
// nothing, with its tools turned off (forkConfig) and its turn given no environment to run them in.
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
	cfg, err := s.forkConfig()
	if err != nil {
		return "", err
	}
	err = s.rpc.Call(ctx, "thread/fork", map[string]any{
		"threadId": id, "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only",
		"developerInstructions": forkInstructions, "config": cfg,
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
	turn := map[string]any{"threadId": r.Thread.ID, "input": input, "environments": []any{}}
	if err := s.rpc.Call(ctx, "turn/start", turn, nil); err != nil {
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

// tell says text to the turn it's about, or holds it for the next prompt.
func (s *session) tell(threadID, turnID, text string) {
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
