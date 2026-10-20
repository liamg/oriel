package host

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
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	_ "github.com/liamg/oriel/sandbox/runner"
	_ "github.com/liamg/oriel/sandbox/seatbelt"
)

// GatewayServer is the gateway's name in the agent's MCP config.
const GatewayServer = "oriel-gw"

// Sandboxes is the SandboxFactory backed by the real sandbox: a sandbox.Backend that confines each agent run, the
// egress proxy's host side, the gateway and the session socket. Attach the host before a session opens one.
type Sandboxes struct {
	// Config is the sandbox's settings; nil means the attached host's, as the app last sent them.
	Config func() sandbox.Config
	// Backend is the backend a config names; sandbox.BackendFor.
	Backend func(cfg sandbox.Config) (sandbox.Backend, error)
	// Available are the backends this machine can run; sandbox.AvailableBackends.
	Available func() []string

	mu sync.Mutex
	w  *Host
}

var _ SandboxFactory = (*Sandboxes)(nil)

// NewSandboxFactory makes the real sandbox factory.
func NewSandboxFactory(config func() sandbox.Config) *Sandboxes {
	return &Sandboxes{Config: config, Backend: sandbox.BackendFor, Available: sandbox.AvailableBackends}
}

func (s *Sandboxes) Backends() []string { return s.Available() }

// Attach gives the factory the host whose sessions it serves.
func (s *Sandboxes) Attach(w *Host) {
	s.mu.Lock()
	s.w = w
	s.mu.Unlock()
}

func (s *Sandboxes) host() *Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w
}

func (s *Sandboxes) config(se core.SessionState) sandbox.Config {
	if s.Config != nil {
		return s.Config()
	}
	if w := s.host(); w != nil {
		return w.SandboxConfig()
	}
	return sandbox.Config{}
}

// box is one session's sandbox.
type box struct {
	f       *Sandboxes
	w       *Host
	id      core.SessionID
	root    string
	approve Approve
	cfg     sandbox.Config
	backend sandbox.Backend

	egress  *egress.Session
	gateway *gateway.Gateway
	ssh     *agentsock.SSHAgent
	mcp     string

	ctx      context.Context
	cancel   context.CancelFunc
	served   sync.WaitGroup
	launched bool
	hook     string // the provider's hook socket on the host
	netShown map[string]bool
	mu       sync.Mutex
}

// Open readies the sandbox for a session's workspace: its record and manifest, the egress session, the gateway,
// the ssh-agent and the session socket. The backend confines the agent when it starts (Exec).
func (s *Sandboxes) Open(ctx context.Context, se core.SessionState, approve Approve) (Sandbox, error) {
	w := s.host()
	if w == nil {
		return nil, errors.New("the sandbox factory has no session host")
	}
	root := agentDir(se)
	if root == "" {
		return nil, errors.New("the session has no workspace to sandbox")
	}
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		return nil, err
	}
	if err := sandbox.SaveManifest(root, manifestOf(root, se)); err != nil {
		return nil, err
	}
	cfg := s.config(se)
	if se.Agent.Network == "open" {
		cfg.NoEgressProxy = true
	}
	if se.Agent.Backend != "" {
		cfg.Backend = se.Agent.Backend
	}
	backend, err := s.Backend(cfg)
	if err != nil {
		return nil, err
	}
	b := &box{f: s, w: w, id: se.ID, root: root, approve: approve, cfg: cfg, backend: backend}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.egress = &egress.Session{
		GrantsPath: sandbox.EgressGrantsPath(root),
		Settings:   b.egressSettings,
		Env: func() []egress.Rule {
			return egress.EnvRules(sandbox.EndpointEnv(os.Environ(), cfg.Env, sandbox.ClaudeSettings()))
		},
		Approver: b,
		Always:   alwaysEgress,
		Failed:   b.netFailed,
	}
	home, _ := os.UserHomeDir()
	up := &gateway.Upstreams{Servers: gateway.Discover(home, cfg.Servers, cfg.Hidden)}
	b.gateway = &gateway.Gateway{
		Builtins: gateway.Builtins(gatewayHost{b}),
		Up:       up,
		Levels:   b.gatewayLevels,
		Approve:  gatewayHost{b}.Approve,
		Grant:    b.grant,
		Always:   func(rules []gateway.Rule, everywhere bool) { _ = sandbox.AddAlwaysRules(rules, everywhere) },
		Log:      b.logCall,
		Timeout:  time.Duration(cfg.ApprovalSecs) * time.Second,
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
	h := &agentsock.Handler{Token: rec.AgentToken, Gateway: b.gateway, Egress: b.egress, SSH: b.ssh, Hook: b.hookSocket}
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
	b.mcp = filepath.Join(sandbox.ClaudeDir(root), "oriel-gw.json")
	if err := writeGatewayMCP(b.mcp); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

// serve runs a socket server until the box closes; an error it meets at once is Open's.
func (b *box) hookSocket() string {
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

func (b *box) serve(run func(ctx context.Context) error) error {
	errc := make(chan error, 1)
	b.served.Add(1)
	go func() {
		defer b.served.Done()
		var err error
		func() {
			defer b.w.recover("a sandbox", &err)
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

// manifestOf is the session's projects as the sandbox records them: worktrees inside the workspace.
func manifestOf(root string, se core.SessionState) sandbox.Manifest {
	var m sandbox.Manifest
	for _, p := range se.Projects {
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil || p.Repo == "" || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		m.Projects = append(m.Projects, sandbox.Project{Name: p.Name, Repo: p.Repo, Dir: rel, Branch: p.Branch, Primary: p.Primary})
	}
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

func (b *box) Spec() *agent.Sandbox {
	return &agent.Sandbox{Name: b.backend.Name() + ":" + sandbox.Key(b.root), GatewayMCP: b.mcp}
}

func (b *box) Exec() agent.Exec { return sandboxExec{b} }

func (b *box) Close() error {
	b.cancel()
	b.served.Wait()
	b.gateway.Up.Close()
	b.ssh.Revoke()
	_ = os.Remove(b.mcp)
	b.mu.Lock()
	launched := b.launched
	b.mu.Unlock()
	if launched {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return b.backend.Stop(ctx, b.root)
	}
	return nil
}

func (b *box) session() (core.SessionState, error) { return b.w.session(b.id) }

func (b *box) egressSettings() [][]egress.Rule {
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
func (b *box) netFailed(ev egress.Event) {
	key := net.JoinHostPort(ev.Host, strconv.Itoa(ev.Port))
	b.mu.Lock()
	shown := b.netShown[key]
	if b.netShown == nil {
		b.netShown = map[string]bool{}
	}
	b.netShown[key] = true
	b.mu.Unlock()
	h, _ := b.w.sessionFor(b.id, false)
	if shown || h == nil {
		return
	}
	it := core.Item{ID: newID(6), Kind: core.ItemSystem, Time: b.w.opts.Now().UTC(), Text: "Network: blocked " + key}
	if ev.Allowed {
		it.Kind, it.Text = core.ItemError, "Network: couldn't reach "+key+": "+ev.Err
	} else if why := cmp.Or(ev.Reason, ev.Err); why != "" {
		it.Text += " (" + why + ")"
	}
	h.appendItem(it)
}

// ApproveEgress is layer 3 for the network: the connection is held while the session's rules, the app's hooks
// or the user decide.
func (b *box) ApproveEgress(ctx context.Context, host string, port int) (egress.Answer, error) {
	hp := net.JoinHostPort(host, strconv.Itoa(port))
	ask := core.Ask{Kind: core.AskNetwork, Host: hp, Summary: "Connect to " + hp, Rule: "net(" + hp + ")",
		Detail: "The sandbox's first connection to " + hp + "; it is held until you answer."}
	v, err := b.approve(ctx, ask)
	if err != nil {
		return egress.Answer{}, err
	}
	if !v.Allow {
		return egress.Answer{Key: "deny", By: v.By, Note: v.Note}, nil
	}
	scope := v.Scope
	if scope != "" {
		h, p := scope, 0
		if i := strings.LastIndex(scope, ":"); i > 0 {
			if n, err := strconv.Atoi(scope[i+1:]); err == nil {
				h, p = scope[:i], n
			}
		}
		_ = alwaysEgress(egress.Rule{Host: h, Port: p, Allow: true}, false)
		_ = b.w.addRule(b.id, "net("+scope+")")
		return egress.Answer{Key: egress.Once}, nil
	}
	if b.ruled(ask.Rule) {
		return egress.Answer{Key: egress.Always}, nil
	}
	return egress.Answer{Key: egress.Once}, nil
}

// ruled reports whether the session has rule: the user answered "always", or had before.
func (b *box) ruled(rule string) bool {
	se, err := b.session()
	return err == nil && rule != "" && slices.Contains(se.Rules, rule)
}

func (b *box) gatewayLevels() gateway.Levels {
	l := gateway.Levels{Session: b.grants(), Global: append(slices.Clone(b.cfg.Rules), sandbox.LoadAlways().Rules...),
		ReadOnly: b.cfg.AutoAllowReadOnly}
	if se, err := b.session(); err == nil {
		for _, p := range se.Projects {
			l.Project = append(l.Project, b.cfg.Projects[p.Repo].Rules...)
		}
	}
	return l
}

func (b *box) grants() []gateway.Rule {
	var out []gateway.Rule
	if data, err := os.ReadFile(sandbox.GrantsPath(b.root)); err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func (b *box) grant(rules []gateway.Rule) {
	have := b.grants()
	for _, r := range rules {
		have = sandbox.PutRule(have, r)
	}
	_ = b.saveGrants(have)
}

// saveGrants replaces the session's gateway grants.
func (b *box) saveGrants(rules []gateway.Rule) error {
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sandbox.GrantsPath(b.root), data, 0o600)
}

func (b *box) logCall(e gateway.Entry) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	if f, err := os.OpenFile(sandbox.CallsPath(b.root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}

// restartWhenIdle restarts the agent once its turn ends, resumed, to pick up a changed sandbox.
func (b *box) restartWhenIdle(why string) {
	go func() {
		defer b.w.recover("a sandbox restart", nil)
		for {
			se, err := b.session()
			if err != nil {
				return
			}
			if !se.Agent.Working {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		h, _ := b.w.sessionFor(b.id, false)
		if h == nil {
			return
		}
		h.stop(why, "")
		_ = h.start()
	}()
}

// sandboxExec runs a provider's processes in the session's sandbox.
type sandboxExec struct{ b *box }

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
func (b *box) wrap(ctx context.Context, cmd *exec.Cmd) (*exec.Cmd, error) {
	if len(cmd.Args) == 0 {
		return nil, errors.New("sandbox: no command")
	}
	argv := append([]string{filepath.Base(cmd.Args[0])}, cmd.Args[1:]...)
	cwd := cmd.Dir
	if cwd == "" {
		cwd = b.root
	}
	o := sandbox.LaunchOptions{ModDir: pluginDir(argv), Environ: os.Environ(), Extra: extraEnv(cmd.Env), Config: b.cfg}
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
	b.launched = true
	b.mu.Unlock()
	out := exec.Command(c.Argv[0], c.Argv[1:]...)
	out.Env, out.Dir = c.Env, c.Dir
	return out, nil
}

func pluginDir(argv []string) string {
	for i, a := range argv {
		if a == "--plugin-dir" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
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
