package sandboxhost

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	_ "github.com/liamg/oriel/sandbox/runner"
	_ "github.com/liamg/oriel/sandbox/seatbelt"
)

// GatewayServer is the gateway's name in the agent's MCP config.
const GatewayServer = "oriel-gw"

// Factory is the SandboxFactory backed by the real sandbox: a sandbox.Backend that confines each agent run, the
// egress proxy's host side, the gateway and the session socket. Attach the host before a session opens one.
type Factory struct {
	// Config is the sandbox's settings; nil means the attached host's, as the app last sent them.
	Config func() sandbox.Config
	// Backend is the backend a config names; sandbox.BackendFor.
	Backend func(cfg sandbox.Config) (sandbox.Backend, error)
	// Available are the backends this machine can run; sandbox.AvailableBackends.
	Available func() []string
	// Helper is the sandbox helper the app carries, for a backend whose sandboxes run it (sandbox.HelperRunner).
	Helper func() ([]byte, error)

	mu       sync.Mutex
	deps     Deps
	attached bool
}

var _ hostcore.SandboxFactory = (*Factory)(nil)

// NewFactory makes the real sandbox factory.
func NewFactory(config func() sandbox.Config) *Factory {
	return &Factory{Config: config, Backend: sandbox.BackendFor, Available: sandbox.AvailableBackends}
}

func (s *Factory) Backends() []string { return s.Available() }

// BackendFor is the backend cfg names.
func (s *Factory) BackendFor(cfg sandbox.Config) (sandbox.Backend, error) {
	if s.Backend == nil {
		return nil, errors.New("no sandbox backends")
	}
	b, err := s.Backend(cfg)
	if h, ok := b.(sandbox.HelperRunner); ok && err == nil {
		b = h.WithHelper(s.Helper)
	}
	return b, err
}

func (s *Factory) Sweep(ctx context.Context, attached, known func(root string) bool) []string {
	cfg := s.config(core.SessionState{})
	var out []string
	for _, name := range s.Available() {
		named := cfg
		named.Backend = name
		if b, err := s.BackendFor(named); err == nil {
			out = append(out, b.Sweep(ctx, attached)...)
		}
	}
	keep := func(root string) bool { return attached(root) || known(root) }
	return append(out, sandbox.Sweep(keep, cfg)...)
}

// Attach gives the factory the host whose sessions it serves.
func (s *Factory) Attach(d Deps) {
	s.mu.Lock()
	s.deps, s.attached = d, true
	s.mu.Unlock()
}

func (s *Factory) host() (Deps, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deps, s.attached
}

func (s *Factory) config(se core.SessionState) sandbox.Config {
	if s.Config != nil {
		return s.Config()
	}
	if d, ok := s.host(); ok && d.Config != nil {
		return d.Config()
	}
	return sandbox.Config{}
}

// Box is one session's sandbox.
type Box struct {
	f       *Factory
	d       Deps
	id      core.SessionID
	root    string
	approve hostcore.Approve
	cfg     sandbox.Config
	backend sandbox.Backend
	// provider names the agent's provider, agent what says what it needs here (nil: nothing past the workspace).
	provider string
	agent    agent.Sandboxed
	stateDir string

	egress  *egress.Session
	gateway *gateway.Gateway
	ssh     *agentsock.SSHAgent
	mcp     string

	ctx        context.Context
	cancel     context.CancelFunc
	served     sync.WaitGroup
	launched   bool
	hook       string // the provider's hook socket on the host
	lastNeeds  agent.SandboxNeeds
	netShown   map[string]bool
	mu         sync.Mutex
	grantsMu   sync.Mutex // one change to the grants file at a time
	restarting bool       // a RestartWhenIdle is pending
}

// Open readies the sandbox for a session's workspace: its record and manifest, the egress session, the gateway,
// the ssh-agent and the session socket. The backend confines the agent when it starts (Exec).
func (s *Factory) Open(ctx context.Context, se core.SessionState, approve hostcore.Approve, a hostcore.SandboxAgent) (hostcore.Sandbox, error) {
	d, ok := s.host()
	if !ok {
		return nil, errors.New("the sandbox factory has no session host")
	}
	root := hostcore.AgentDir(se)
	if root == "" {
		return nil, errors.New("the session has no workspace to sandbox")
	}
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		return nil, err
	}
	if err := sandbox.SaveManifest(root, ManifestOf(root, se)); err != nil {
		return nil, err
	}
	cfg := s.config(se)
	if se.Agent.Network == "open" {
		cfg.NoEgressProxy = true
	}
	if se.Agent.Backend != "" {
		cfg.Backend = se.Agent.Backend
	}
	backend, err := s.BackendFor(cfg)
	if err != nil {
		return nil, err
	}
	b := &Box{f: s, d: d, id: se.ID, root: root, approve: approve, cfg: cfg, backend: backend, stateDir: a.StateDir}
	if a.Provider != nil {
		b.provider = a.Provider.ID()
		b.agent, _ = a.Provider.(agent.Sandboxed)
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.egress = &egress.Session{
		GrantsPath: sandbox.EgressGrantsPath(root),
		Settings:   b.egressSettings,
		Env: func() []egress.Rule {
			return egress.EnvRules(sandbox.EndpointEnv(os.Environ(), cfg.Env, b.needs()))
		},
		Agent:    func() []egress.Rule { return egress.HostRules(b.needs().Hosts) },
		Approver: b,
		Open:     cfg.NoEgressProxy,
		Always:   alwaysEgress,
		SaveFailed: func(r egress.Rule, err error) {
			b.notSaved("Network: "+net.JoinHostPort(r.Host, strconv.Itoa(r.Port)), err)
		},
		Failed: b.netFailed,
	}
	var providers []agent.Provider
	if d.Providers != nil {
		providers = d.Providers()
	}
	up := &gateway.Upstreams{Servers: gateway.Found(providers, cfg.Servers, cfg.Hidden), Cache: sandbox.ToolsCache()}
	b.gateway = &gateway.Gateway{
		Builtins: gateway.Builtins(gatewayHost{b}),
		Up:       up,
		Levels:   b.gatewayLevels,
		Approve:  gatewayHost{b}.Approve,
		Grant:    b.grant,
		Always: func(rules []gateway.Rule, everywhere bool) error {
			err := sandbox.AddAlwaysRules(rules, everywhere)
			if err != nil {
				tools := make([]string, len(rules))
				for i, r := range rules {
					tools[i] = r.Tool
				}
				b.notSaved("Tools: "+strings.Join(tools, ", "), err)
			}
			return err
		},
		Log:     b.logCall,
		Timeout: time.Duration(cfg.ApprovalSecs) * time.Second,
	}
	b.ssh = &agentsock.SSHAgent{}
	if err := os.MkdirAll(sandbox.RunDir(root), 0o700); err != nil {
		b.cancel()
		return nil, err
	}
	rec, _ := sandbox.Load(root)
	portFile := ""
	if backend.ServesPort() {
		portFile = sandbox.PortPath(root)
	}
	h := &agentsock.Handler{Token: rec.AgentToken, Gateway: b.gateway, Egress: b.egress, SSH: b.ssh, Hook: b.hookSocket, ToolsVersion: up.Version}
	if err := b.serve(func(ctx context.Context) error { return agentsock.Serve(ctx, h, sandbox.SocketPath(root), portFile) }); err != nil {
		b.cancel()
		return nil, err
	}
	if err := b.serve(func(ctx context.Context) error {
		return b.ssh.Serve(ctx, filepath.Join(sandbox.RunDir(root), "ssh-agent.sock"))
	}); err != nil {
		b.Close()
		return nil, err
	}
	if err := b.serve(func(ctx context.Context) error {
		return serveForward(ctx, filepath.Join(sandbox.RunDir(root), sandbox.HookSocket), b.hookSocket)
	}); err != nil {
		b.Close()
		return nil, err
	}
	b.mcp = filepath.Join(sandbox.AgentDir(root, cmp.Or(b.provider, "agent")), "oriel-gw.json")
	if err := writeGatewayMCP(b.mcp); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

// hookSocket is the provider's hook socket on the host.
func (b *Box) hookSocket() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hook
}

// serveForward listens on path until ctx ends, carrying each connection to the unix socket to() names then.
func serveForward(ctx context.Context, path string, to func() string) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, func() { _ = ln.Close() })
	for {
		c, err := ln.Accept()
		if err != nil {
			return nil
		}
		go func() {
			defer c.Close()
			up, err := net.Dial("unix", to())
			if err != nil {
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
			go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
			<-done
			<-done
		}()
	}
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// serve runs a socket server until the box closes; an error it meets at once is Open's.
func (b *Box) serve(run func(ctx context.Context) error) error {
	errc := make(chan error, 1)
	b.served.Add(1)
	go func() {
		defer b.served.Done()
		var err error
		func() {
			defer b.d.Env.Recover("a sandbox", &err)
			err = run(b.ctx)
		}()
		errc <- err
	}()
	select {
	case err := <-errc:
		if err == nil {
			err = errors.New("the socket closed at once")
		}
		return err
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// ManifestOf is the session's projects as the sandbox records them: worktrees inside the workspace.
func ManifestOf(root string, se core.SessionState) sandbox.Manifest {
	var m sandbox.Manifest
	for _, p := range se.Projects {
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil || p.Repo == "" {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, "../") {
			// A checkout read in place, with no worktree here yet: the agent reads it, read-only.
			m.Reads = append(m.Reads, p.Dir)
			continue
		}
		m.Projects = append(m.Projects, sandbox.Project{Name: p.Name, Repo: p.Repo, Dir: rel, Branch: p.Branch, Primary: p.Primary})
	}
	m.Memory = hostcore.Memories(se)
	return m
}

func writeGatewayMCP(path string) error {
	cfg := map[string]any{"mcpServers": map[string]any{GatewayServer: map[string]any{"command": "oriel", "args": []string{"mcp"}}}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (b *Box) Spec() *agent.Sandbox {
	return &agent.Sandbox{Name: b.backend.Name() + ":" + sandbox.Key(b.root), GatewayMCP: b.mcp,
		HookSocket: filepath.Join(b.backend.AgentRunDir(b.root), sandbox.HookSocket)}
}

func (b *Box) Exec() agent.Exec { return sandboxExec{b} }

func (b *Box) Close() error {
	b.cancel()
	b.served.Wait()
	b.gateway.Up.Close()
	b.ssh.Revoke()
	_ = os.Remove(b.mcp)
	b.mu.Lock()
	launched := b.launched
	b.mu.Unlock()
	var err error
	if launched {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = b.backend.Stop(ctx, b.root)
	}
	return errors.Join(err, sandbox.EmptyTmp(b.root))
}

func (b *Box) session() (core.SessionState, error) { return b.d.State.Session(b.id) }

func (b *Box) egressSettings() [][]egress.Rule {
	var out [][]egress.Rule
	if se, err := b.session(); err == nil {
		var project []egress.Rule
		for _, p := range se.Projects {
			project = append(project, egressRules(SourceProject, b.cfg.Projects[p.Repo].Egress)...)
		}
		out = append(out, project)
	}
	return append(out, append(egressRules(SourceSettings, b.cfg.Egress), egressRules(SourceAlways, sandbox.LoadAlways().Egress)...))
}

func egressRules(source string, rs []sandbox.EgressRule) []egress.Rule {
	out := make([]egress.Rule, 0, len(rs))
	for _, r := range rs {
		out = append(out, egress.Rule{Host: r.Host, Port: r.Port, Allow: r.Allow, Private: r.Private, Source: source})
	}
	return out
}

func alwaysEgress(r egress.Rule, everywhere bool) error {
	return sandbox.AddAlwaysEgress(sandbox.EgressRule{Host: r.Host, Port: r.Port, Allow: r.Allow, Private: r.Private}, everywhere)
}

// netFailed puts a connection that never got through in the session's transcript, once per destination, so a
// stuck agent says why without its terminal.
func (b *Box) netFailed(ev egress.Event) {
	key := net.JoinHostPort(ev.Host, strconv.Itoa(ev.Port))
	b.mu.Lock()
	shown := b.netShown[key]
	if b.netShown == nil {
		b.netShown = map[string]bool{}
	}
	b.netShown[key] = true
	b.mu.Unlock()
	if shown {
		return
	}
	it := core.Item{ID: hostcore.NewID(6), Kind: core.ItemSystem, Time: b.d.Env.Now().UTC(), Text: "Network: blocked " + key}
	if ev.Allowed {
		it.Kind, it.Text = core.ItemError, "Network: couldn't reach "+key+": "+ev.Err
	} else if why := cmp.Or(ev.Reason, ev.Err); why != "" {
		it.Text += " (" + why + ")"
	}
	b.d.Note(b.id, it)
}

// notSaved puts an allow from a card that couldn't be kept in the session's transcript.
func (b *Box) notSaved(what string, err error) {
	b.d.Env.Log.Warn("sandbox: an allow was not saved", "session", b.id, "what", what, "err", err)
	b.d.Note(b.id, core.Item{ID: hostcore.NewID(6), Kind: core.ItemError, Time: b.d.Env.Now().UTC(),
		Text: what + ": allowed, but the rule was not saved: " + err.Error()})
}

// ApproveEgress is layer 3 for the network: the connection is held while the session's rules, the app's hooks
// or the user decide.
func (b *Box) ApproveEgress(ctx context.Context, host string, port int) (egress.Answer, error) {
	hp := net.JoinHostPort(host, strconv.Itoa(port))
	ask := core.Ask{Kind: core.AskNetwork, Host: hp, Summary: "Connect to " + hp, Rule: "net(" + hp + ")",
		Detail: "The agent is trying to connect to " + hp + " for the first time; it waits until you answer."}
	v, err := b.approve(ctx, ask)
	if err != nil {
		return egress.Answer{}, err
	}
	if !v.Allow {
		return egress.Answer{Key: "deny", By: v.By, Note: v.Note}, nil
	}
	// An always for all sessions is the host's rule, met before any card; one for the session is its grant too.
	if scope := v.Scope; scope != "" && !v.All && b.ruled("net("+scope+")") {
		h, p := scope, 0
		if i := strings.LastIndex(scope, ":"); i > 0 {
			if n, err := strconv.Atoi(scope[i+1:]); err == nil {
				h, p = scope[:i], n
			}
		}
		if err := b.egress.SetRule(h, p, true, false); err != nil {
			b.notSaved("Network: "+scope, err)
		}
		return egress.Answer{Key: egress.Once}, nil
	}
	if !v.All && b.ruled(ask.Rule) {
		return egress.Answer{Key: egress.ForSession}, nil
	}
	return egress.Answer{Key: egress.Once}, nil
}

// ruled reports whether the session has rule: the user answered "always", or had before.
func (b *Box) ruled(rule string) bool {
	se, err := b.session()
	return err == nil && rule != "" && slices.Contains(se.Rules, rule)
}

func (b *Box) gatewayLevels() gateway.Levels {
	l := gateway.Levels{Session: b.grants(), Global: append(slices.Clone(b.cfg.Rules), sandbox.LoadAlways().Rules...),
		ReadOnly: b.cfg.AutoAllowReadOnly}
	if se, err := b.session(); err == nil {
		for _, p := range se.Projects {
			l.Project = append(l.Project, b.cfg.Projects[p.Repo].Rules...)
		}
	}
	return l
}

func (b *Box) grants() []gateway.Rule {
	var out []gateway.Rule
	if data, err := os.ReadFile(sandbox.GrantsPath(b.root)); err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func (b *Box) grant(rules []gateway.Rule) {
	err := b.updateGrants(func(have []gateway.Rule) []gateway.Rule {
		for _, r := range rules {
			have = sandbox.PutRule(have, r)
		}
		return have
	})
	if err != nil {
		b.d.Env.Log.Warn("sandbox: saving grants", "session", b.id, "err", err)
	}
}

// updateGrants replaces the session's gateway grants with what fn makes of them, one change at a time.
func (b *Box) updateGrants(fn func([]gateway.Rule) []gateway.Rule) error {
	b.grantsMu.Lock()
	defer b.grantsMu.Unlock()
	data, err := json.MarshalIndent(fn(b.grants()), "", "  ")
	if err != nil {
		return err
	}
	path := sandbox.GrantsPath(b.root)
	f, err := os.CreateTemp(filepath.Dir(path), ".grants-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}

func (b *Box) logCall(e gateway.Entry) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	if f, err := os.OpenFile(sandbox.CallsPath(b.root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}

// RestartWhenIdle restarts the agent once its turn ends, resumed, to pick up a changed sandbox. Asked again before
// then, it still restarts once; a box closed meanwhile (the agent stopped) restarts nothing.
func (b *Box) RestartWhenIdle(why string) {
	b.mu.Lock()
	if b.restarting {
		b.mu.Unlock()
		return
	}
	b.restarting = true
	b.mu.Unlock()
	b.d.RestartWhenIdle(b.id, b, why)
}

// sandboxExec runs a provider's processes in the session's sandbox.
type sandboxExec struct{ b *Box }

func (e sandboxExec) StartPTY(ctx context.Context, cmd *exec.Cmd, cols, rows int) (agent.PTY, error) {
	wrapped, err := e.b.wrap(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return hostexec.Exec{}.StartPTY(ctx, wrapped, cols, rows)
}

func (e sandboxExec) Start(ctx context.Context, cmd *exec.Cmd) (agent.Process, error) {
	wrapped, err := e.b.wrap(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return hostexec.Exec{}.Start(ctx, wrapped)
}

// wrap turns a provider's command into the backend's command that runs it in the sandbox.
func (b *Box) wrap(ctx context.Context, cmd *exec.Cmd) (*exec.Cmd, error) {
	if len(cmd.Args) == 0 {
		return nil, errors.New("sandbox: no command")
	}
	argv := append([]string{filepath.Base(cmd.Args[0])}, cmd.Args[1:]...)
	cwd := cmd.Dir
	if cwd == "" {
		cwd = b.root
	}
	o := sandbox.LaunchOpts{Environ: os.Environ(), Extra: extraEnv(cmd.Env), Config: b.cfg, Provider: b.provider, Agent: b.agent,
		StateDir: b.stateDir}
	if hook := o.Extra[agent.HookSocketEnv]; hook != "" {
		b.mu.Lock()
		b.hook = hook
		b.mu.Unlock()
		o.Extra[agent.HookSocketEnv] = filepath.Join(b.backend.AgentRunDir(b.root), sandbox.HookSocket)
	}
	c, err := b.backend.Launch(ctx, b.root, cwd, argv, o)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	b.mu.Lock()
	b.launched, b.lastNeeds = true, c.Needs
	b.mu.Unlock()
	out := exec.Command(c.Argv[0], c.Argv[1:]...)
	out.Env, out.Dir, out.Stderr = c.Env, c.Dir, cmd.Stderr
	return out, nil
}

// needs are what the agent's provider said it needs at its last launch.
func (b *Box) needs() agent.SandboxNeeds {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastNeeds
}

// extraEnv is what a provider added to the environment: the sandbox gets that, not the host's own.
// oriel is on the sandbox's PATH.
func extraEnv(env []string) map[string]string {
	host := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		host[k] = v
	}
	out := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if old, had := host[k]; had && old == v {
			continue
		}
		out[k] = v
	}
	if _, ok := out["ORIEL_BIN"]; ok {
		out["ORIEL_BIN"] = "oriel"
	}
	return out
}
