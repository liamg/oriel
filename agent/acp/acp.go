// Package acp is an agent.Provider for any agent that speaks the Agent Client Protocol (Gemini CLI, goose,
// opencode, claude-code-acp…): it runs the agent through the session's Exec and is its ACP client over stdio
// (spec/acp). Oriel's tools reach the agent as the MCP server oriel, and the sandbox's gateway with them.
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/mcptools"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// Version is the ACP protocol version this client speaks.
const Version = 1

// CodeAuthRequired is ACP's error for a request that needs the agent signed in.
const CodeAuthRequired = -32000

// Config is how to run an ACP agent: what a plugin's oriel.agent.provider{ acp = … } says.
type Config struct {
	Command []string          `json:"command"` // the agent and its arguments: { "gemini", "--experimental-acp" }
	Env     map[string]string `json:"env,omitempty"`
	Models  []agent.Model     `json:"models,omitempty"` // what to offer; the agent's own default alone if none
	Login   string            `json:"login,omitempty"`  // what to run in a shell to sign in: "gemini"; Command if ""
	// What the agent needs in a sandbox: the hosts it reaches, the tools a container installs to run it, the host
	// variables it inherits (names or NAME_* globs), and the entries of the user's home it keeps its config and
	// sign-in in (".gemini"), which it sees at the sandbox's home. The OS's sandbox runs the host's Command[0].
	Hosts   []string `json:"hosts,omitempty"`
	Tools   []string `json:"tools,omitempty"` // what a container installs for it with mise: "npm:@google/gemini-cli@0.9.0"
	PassEnv []string `json:"pass_env,omitempty"`
	Home    []string `json:"home,omitempty"`
	Self    string   `json:"-"` // the binary that serves `oriel hook mcp`; os.Executable if ""
}

// Provider runs Config's agent, one process a session.
type Provider struct {
	Name   string // its ID
	Config Config
}

var (
	_ agent.Provider = (*Provider)(nil)
	_ agent.Featured = (*Provider)(nil)
)

// FromConfig reads a provider's config as JSON: what a bridge is handed.
func FromConfig(name string, raw json.RawMessage) (*Provider, error) {
	var c Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("acp config: %w", err)
		}
	}
	if len(c.Command) == 0 || c.Command[0] == "" {
		return nil, errors.New("acp config: no command to run the agent")
	}
	if name == "" {
		name = c.Command[0]
	}
	return &Provider{Name: name, Config: c}, nil
}

func (p *Provider) ID() string { return p.Name }

// Features: an ACP agent has no terminal Oriel can show and nothing to fork from; it runs in a sandbox.
func (p *Provider) Features() agent.Features { return agent.Features{Sandbox: true} }

// Detect finds the agent's program. ACP has no way to ask who is signed in short of starting a session, so it
// takes the agent to be signed in until one says otherwise (AuthExpired).
func (p *Provider) Detect(context.Context) (agent.Install, error) {
	path, err := exec.LookPath(p.Config.Command[0])
	if err != nil {
		return agent.Install{}, fmt.Errorf("%s isn't installed: %w", p.Config.Command[0], err)
	}
	return agent.Install{Bin: path, LoggedIn: true}, nil
}

func (p *Provider) Models(context.Context) ([]agent.Model, error) {
	if len(p.Config.Models) > 0 {
		return slices.Clone(p.Config.Models), nil
	}
	return []agent.Model{{ID: "default", Name: "Default", Default: true}}, nil
}

func (p *Provider) Usage(context.Context) (core.ProviderUsage, error) {
	return core.ProviderUsage{Provider: p.Name}, nil
}

func (p *Provider) Start(ctx context.Context, spec agent.StartSpec) (agent.Session, error) {
	return p.start(ctx, "", spec)
}

func (p *Provider) Resume(ctx context.Context, id string, spec agent.StartSpec) (agent.Session, error) {
	return p.start(ctx, id, spec)
}

// signedOut is the agent's login gone: it signs in again from a shell.
func (p *Provider) signedOut(detail string) agent.AuthExpired {
	shell := p.Config.Login
	if shell == "" {
		shell = strings.Join(p.Config.Command, " ")
	}
	return agent.AuthExpired{Provider: p.Name, Detail: detail, Shell: shell}
}

// failed is err from the agent, as Oriel says it: a signed-out error is AuthExpired.
func (p *Provider) failed(what string, err error) error {
	var je *jsonrpc.Error
	if errors.As(err, &je) && je.Code == CodeAuthRequired {
		return p.signedOut(je.Message)
	}
	return fmt.Errorf("%s %s: %w", p.Name, what, err)
}

type agentCaps struct {
	LoadSession        bool `json:"loadSession"`
	PromptCapabilities struct {
		Image bool `json:"image"`
	} `json:"promptCapabilities"`
	MCPCapabilities struct {
		HTTP bool `json:"http"`
		SSE  bool `json:"sse"`
	} `json:"mcpCapabilities"`
	SessionCapabilities struct {
		Resume json.RawMessage `json:"resume"`
		Close  json.RawMessage `json:"close"`
	} `json:"sessionCapabilities"`
}

func has(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

type configOption struct {
	ID           string          `json:"id"`
	Category     string          `json:"category"`
	Type         string          `json:"type"`
	CurrentValue json.RawMessage `json:"currentValue"`
	Options      []struct {
		Value   string `json:"value"`
		Options []struct {
			Value string `json:"value"`
		} `json:"options"`
	} `json:"options"`
}

// offers reports whether a select option has value among its values, grouped or not.
func (o configOption) offers(value string) bool {
	for _, v := range o.Options {
		if v.Value == value || slices.ContainsFunc(v.Options, func(g struct {
			Value string `json:"value"`
		}) bool {
			return g.Value == value
		}) {
			return true
		}
	}
	return false
}

// modelOf is the model the session runs: the one asked for when the agent offers it, else the agent's current one.
func modelOf(opts []configOption, asked string) string {
	for _, o := range opts {
		if o.Category != "model" {
			continue
		}
		if asked != "" && o.offers(asked) {
			return asked
		}
		var cur string
		json.Unmarshal(o.CurrentValue, &cur)
		return cur
	}
	return ""
}

func (p *Provider) start(ctx context.Context, resume string, spec agent.StartSpec) (agent.Session, error) {
	tools, err := mcptools.Serve(spec.Offered)
	if err != nil {
		return nil, fmt.Errorf("%s: serving Oriel's tools: %w", p.Name, err)
	}
	c := p.Config.Command
	cmd := exec.Command(c[0], c[1:]...)
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), spec.Env...)
	for k, v := range p.Config.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Env = append(cmd.Env, mcptools.SockEnv+"="+tools.Path())
	errs := &tail{}
	cmd.Stderr = errs
	ex := spec.Exec
	if ex == nil {
		ex = hostexec.Exec{}
	}
	proc, err := ex.Start(ctx, cmd)
	if err != nil {
		tools.Close()
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	s := newSession(p, spec, ex, proc, tools, errs)
	fail := func(err error) (agent.Session, error) {
		s.Close()
		if t := errs.String(); t != "" && !errors.As(err, new(agent.AuthExpired)) {
			err = fmt.Errorf("%w\n%s", err, t)
		}
		return nil, err
	}
	var init struct {
		ProtocolVersion   int       `json:"protocolVersion"`
		AgentCapabilities agentCaps `json:"agentCapabilities"`
	}
	ictx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err = s.rpc.Call(ictx, "initialize", map[string]any{
		"protocolVersion": Version,
		"clientCapabilities": map[string]any{
			"fs":          map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal":    spec.Sandbox != nil,
			"elicitation": map[string]any{"form": map[string]any{}},
		},
		"clientInfo": map[string]any{"name": "oriel", "title": "Oriel", "version": "0"},
	}, &init)
	if err != nil {
		return fail(p.failed("initialize", err))
	}
	if init.ProtocolVersion != Version {
		return fail(fmt.Errorf("%s speaks ACP version %d; Oriel speaks %d", p.Name, init.ProtocolVersion, Version))
	}
	s.caps = init.AgentCapabilities
	servers, err := s.mcpServers()
	if err != nil {
		return fail(err)
	}
	params := map[string]any{"cwd": spec.Cwd, "mcpServers": servers}
	var r struct {
		SessionID     string         `json:"sessionId"`
		ConfigOptions []configOption `json:"configOptions"`
	}
	switch {
	case resume == "":
		err = s.rpc.Call(ctx, "session/new", params, &r)
	case s.caps.LoadSession:
		params["sessionId"] = resume
		s.setReplaying(true)
		err = s.rpc.Call(ctx, "session/load", params, &r)
		s.setReplaying(false)
		r.SessionID = resume
	case has(s.caps.SessionCapabilities.Resume):
		params["sessionId"] = resume
		err = s.rpc.Call(ctx, "session/resume", params, &r)
		r.SessionID = resume
	default:
		return fail(fmt.Errorf("%s can't resume a session", p.Name))
	}
	if err != nil {
		return fail(p.failed("session", err))
	}
	if r.SessionID == "" {
		return fail(fmt.Errorf("%s gave the session no id", p.Name))
	}
	s.mu.Lock()
	s.id, s.history = r.SessionID, resume != ""
	s.mu.Unlock()
	for cat, want := range map[string]string{"model": spec.Model, "thought_level": spec.Effort} {
		if want == "" {
			continue
		}
		for _, o := range r.ConfigOptions {
			if o.Category == cat && o.offers(want) {
				if err := s.rpc.Call(ctx, "session/set_config_option", map[string]any{"sessionId": r.SessionID, "configId": o.ID,
					"value": want}, nil); err != nil {
					return fail(p.failed("set "+cat, err))
				}
				break
			}
		}
	}
	s.emit(agent.Ready{ID: r.SessionID, Model: modelOf(r.ConfigOptions, spec.Model)})
	if spec.Prompt != "" {
		if err := s.Send(ctx, agent.Input{Text: spec.Prompt}); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

// mcpServers are what the session's agent connects to: Oriel's tools, then the sandbox's gateway.
func (s *session) mcpServers() ([]any, error) {
	e := mcptools.Entry(s.tools.Path(), s.p.Config.Self, s.spec.Sandbox)
	out := []any{stdioServer(e.Name, e.Command, e.Args, e.Env)}
	if s.spec.Sandbox == nil || s.spec.Sandbox.GatewayMCP == "" {
		return out, nil
	}
	b, err := os.ReadFile(s.spec.Sandbox.GatewayMCP)
	if err != nil {
		return nil, fmt.Errorf("%s: the gateway's MCP config: %w", s.p.Name, err)
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
		return nil, fmt.Errorf("%s: the gateway's MCP config: %w", s.p.Name, err)
	}
	names := make([]string, 0, len(f.MCPServers))
	for n := range f.MCPServers {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		m := f.MCPServers[name]
		switch {
		case m.URL == "":
			out = append(out, stdioServer(name, m.Command, m.Args, m.Env))
		case (m.Type == "" || m.Type == "http") && s.caps.MCPCapabilities.HTTP:
			out = append(out, map[string]any{"type": "http", "name": name, "url": m.URL, "headers": pairs(m.Headers)})
		case m.Type == "sse" && s.caps.MCPCapabilities.SSE:
			out = append(out, map[string]any{"type": "sse", "name": name, "url": m.URL, "headers": pairs(m.Headers)})
		}
	}
	return out, nil
}

func stdioServer(name, command string, args []string, env map[string]string) map[string]any {
	if args == nil {
		args = []string{}
	}
	return map[string]any{"name": name, "command": command, "args": args, "env": pairs(env)}
}

// pairs are a map as ACP's lists of { name, value }, in name order.
func pairs(m map[string]string) []map[string]string {
	out := []map[string]string{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, map[string]string{"name": k, "value": m[k]})
	}
	return out
}

// tail keeps the end of what the agent wrote to stderr, for an error that needs it.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8<<10 {
		t.buf = t.buf[len(t.buf)-4<<10:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
