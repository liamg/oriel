package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/sandbox/mcp"
)

// sandboxRig is a rig whose sessions run sandboxed, with the container launch faked: Docker isn't needed for
// anything but the container itself.
type sandboxRig struct {
	*rig
	f        *Sandboxes
	remote   string
	launched chan []string
	opts     chan sandbox.LaunchOptions
}

// fakeBackend launches an echo in place of the agent.
type fakeBackend struct{ s *sandboxRig }

func (fakeBackend) Name() string { return "fake" }

func (f fakeBackend) Launch(_ context.Context, root, cwd string, argv []string, o sandbox.LaunchOptions) (sandbox.Command, error) {
	f.s.launched <- argv
	f.s.opts <- o
	return sandbox.Command{Argv: []string{"echo", "in the container"}, Env: os.Environ()}, nil
}

func (fakeBackend) Available() error { return nil }

func (fakeBackend) Stop(context.Context, string) error { return nil }

func (fakeBackend) Health(context.Context, sandbox.Config) []sandbox.Problem { return nil }

func (fakeBackend) Sweep(context.Context, func(string) bool) []string { return nil }

func (fakeBackend) AgentRunDir(string) string { return "/run/oriel" }

func (fakeBackend) ServesPort() bool { return false }

func newSandboxRig(t *testing.T) *sandboxRig {
	t.Helper()
	// The session socket lives under $HOME; a short one keeps its path under the unix limit.
	home, err := os.MkdirTemp("/tmp", "oh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	r := newRig(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	r.repo.Git("init", "-q", "--bare", remote)
	r.repo.Git("remote", "add", "origin", remote)
	s := &sandboxRig{rig: r, remote: remote, launched: make(chan []string, 4), opts: make(chan sandbox.LaunchOptions, 4)}
	s.f = NewSandboxFactory(nil)
	s.f.Backend = func(sandbox.Config) (sandbox.Backend, error) { return fakeBackend{s}, nil }
	s.f.Available = func() []string { return []string{"fake"} }
	s.f.Attach(r.w)
	r.w.opts.Sandbox = s.f
	return s
}

func (s *sandboxRig) sandboxed() (core.SessionID, *agent.StartSpec, *box) {
	s.t.Helper()
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	h, _ := s.w.hostFor(id, false)
	h.mu.Lock()
	b := h.box.(*box)
	h.mu.Unlock()
	return id, &a.Spec, b
}

// waitAsk waits for the session's one ask of kind.
func (s *sandboxRig) waitAsk(id core.SessionID, kind core.AskKind) core.Ask {
	s.t.Helper()
	se := s.eventually(id, "a "+string(kind)+" ask", func(se core.SessionState) bool {
		return len(se.Asks) == 1 && se.Asks[0].Kind == kind
	})
	return se.Asks[0]
}

// get fetches url through the egress proxy, as the sandbox's HTTP_PROXY would.
func get(t *testing.T, proxy, url string) (int, string) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", url, strings.TrimPrefix(url, "http://"))
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// gatewayCall calls a gateway tool over the session socket, as `oriel mcp` in the container does.
func gatewayCall(t *testing.T, root, tool string, args any) mcp.CallResult {
	t.Helper()
	rec, _ := sandbox.Load(root)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sandbox.SocketPath(root))
	}}}
	a, _ := json.Marshal(args)
	body, _ := json.Marshal(map[string]any{"name": tool, "arguments": json.RawMessage(a)})
	req, _ := http.NewRequest(http.MethodPost, "http://oriel"+agentsock.CallPath, bytes.NewReader(body))
	req.Header.Set(agentsock.TokenHeader, rec.AgentToken)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out mcp.CallResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A session runs in the backend it was started with, one of those its worker offers, over sandbox.backend.
func TestSandboxBackendPerSession(t *testing.T) {
	s := newSandboxRig(t)
	s.f.Available = func() []string { return []string{"docker", "seatbelt"} }
	chose := make(chan string, 4)
	s.f.Backend = func(cfg sandbox.Config) (sandbox.Backend, error) { chose <- cfg.Backend; return fakeBackend{s}, nil }
	if got := s.w.workerState().Sandboxes; !slices.Equal(got, []string{"docker", "seatbelt"}) {
		t.Errorf("the worker offers %v", got)
	}
	spec := core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask", Backend: "podman"}}
	if _, err := s.w.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "can't run a podman sandbox") {
		t.Errorf("a backend the worker lacks: %v", err)
	}
	spec.Sandbox.Backend = "seatbelt"
	id := s.start(spec)
	select {
	case got := <-chose:
		if got != "seatbelt" {
			t.Errorf("the session runs in %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no sandbox opened")
	}
	if se, _ := s.w.session(id); se.Agent.Backend != "seatbelt" {
		t.Errorf("the session keeps %q", se.Agent.Backend)
	}
}

func TestSandboxSpecNamesTheGateway(t *testing.T) {
	s := newSandboxRig(t)
	_, spec, b := s.sandboxed()
	if spec.Sandbox == nil || spec.Sandbox.Name != "fake:"+sandbox.Key(b.root) {
		t.Fatalf("spec.Sandbox = %+v", spec.Sandbox)
	}
	data, err := os.ReadFile(spec.Sandbox.GatewayMCP)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	json.Unmarshal(data, &cfg)
	if gw := cfg.MCPServers["oriel-gw"]; gw.Command != "oriel" || len(gw.Args) != 1 || gw.Args[0] != "mcp" {
		t.Errorf("gateway MCP config = %s", data)
	}
	if !sandbox.Enabled(b.root) {
		t.Error("the workspace has no sandbox record")
	}
	if m, _ := sandbox.LoadManifest(b.root); len(m.Projects) != 1 || filepath.IsAbs(m.Projects[0].Dir) {
		t.Errorf("manifest = %+v", m)
	}
}

// The provider's command runs in the container: runner.Launch gets the agent's argv and the env the provider
// added, and the docker command it returns is what runs.
func TestSandboxExecRunsTheLaunchCommand(t *testing.T) {
	s := newSandboxRig(t)
	_, spec, _ := s.sandboxed()
	cmd := exec.Command("claude", "--plugin-dir", "/state/claude-mod", "--permission-mode", "bypassPermissions")
	cmd.Env = append(os.Environ(), "ORIEL_HOOK_SOCK=/tmp/oriel-1/hook.sock", "ORIEL_BIN=/home/me/bin/oriel")
	p, err := spec.Exec.Start(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(p.Stdout())
	p.Wait()
	if strings.TrimSpace(string(out)) != "in the container" {
		t.Errorf("ran %q", out)
	}
	argv, o := <-s.launched, <-s.opts
	if argv[0] != "claude" || o.ModDir != "/state/claude-mod" {
		t.Errorf("argv %v, mod %q", argv, o.ModDir)
	}
	if o.Extra["ORIEL_HOOK_SOCK"] != "/run/oriel/hook.sock" || o.Extra["ORIEL_BIN"] != "oriel" || o.Extra["PATH"] != "" {
		t.Errorf("extra env = %v", o.Extra)
	}
}

// The agent's hooks call back over the provider's socket on the host; in the container that socket is the run
// dir's hook.sock (mounted on Linux, relayed by the sidecar elsewhere), carried to the provider's.
func TestSandboxCarriesTheHookSocket(t *testing.T) {
	s := newSandboxRig(t)
	_, spec, b := s.sandboxed()
	dir, err := os.MkdirTemp("/tmp", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	provider, err := net.Listen("unix", filepath.Join(dir, "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	go func() {
		for {
			c, err := provider.Accept()
			if err != nil {
				return
			}
			go func() {
				line, _ := bufio.NewReader(c).ReadString('\n')
				io.WriteString(c, "hook got "+line)
				c.Close()
			}()
		}
	}()
	cmd := exec.Command("claude")
	cmd.Env = append(os.Environ(), "ORIEL_HOOK_SOCK="+filepath.Join(dir, "hook.sock"))
	p, err := spec.Exec.Start(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(p.Stdout())
	p.Wait()
	<-s.launched
	<-s.opts
	roundTrip := func(how string, c net.Conn) {
		t.Helper()
		defer c.Close()
		io.WriteString(c, "PreToolUse\n")
		got, _ := io.ReadAll(c)
		if string(got) != "hook got PreToolUse\n" {
			t.Errorf("%s: %q", how, got)
		}
	}
	mounted, err := net.Dial("unix", filepath.Join(sandbox.RunDir(b.root), "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip("the run dir's socket", mounted)
	rec, _ := sandbox.Load(b.root)
	raw, err := net.Dial("unix", sandbox.SocketPath(b.root))
	if err != nil {
		t.Fatal(err)
	}
	relayed, err := egress.Upgrade(context.Background(), raw, rec.AgentToken, agentsock.HookPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip("the relay's route", relayed)
}

// agents.md → Permissions, layer 3: an unknown host's connection is held while the user decides.
func TestNetworkAsksHoldTheConnection(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "upstream") }))
	defer up.Close()
	proxy := httptest.NewServer(&egress.Proxy{Decider: b.egress})
	defer proxy.Close()
	paddr := strings.TrimPrefix(proxy.URL, "http://")
	host := strings.TrimPrefix(up.URL, "http://")

	type result struct {
		code int
		body string
	}
	fetch := func() chan result {
		ch := make(chan result, 1)
		go func() {
			code, body := get(t, paddr, up.URL)
			ch <- result{code, body}
		}()
		return ch
	}

	// Deny: the proxy refuses it.
	res := fetch()
	ask := s.waitAsk(id, core.AskNetwork)
	if ask.Host != host || ask.Rule != "net("+host+")" {
		t.Fatalf("ask = %+v", ask)
	}
	select {
	case r := <-res:
		t.Fatalf("not held: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "deny"})
	if r := <-res; r.code != http.StatusForbidden || !strings.Contains(r.body, "not allowed") {
		t.Errorf("denied: %+v", r)
	}

	// Allow: the dial goes through, and the next asks again.
	res = fetch()
	ask = s.waitAsk(id, core.AskNetwork)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "allow"})
	if r := <-res; r.code != 200 || r.body != "upstream" {
		t.Errorf("allowed: %+v", r)
	}

	// Always: recorded, and the next goes through with no ask.
	res = fetch()
	ask = s.waitAsk(id, core.AskNetwork)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "always"})
	if r := <-res; r.code != 200 {
		t.Errorf("always: %+v", r)
	}
	if se := s.session(id); len(se.Rules) != 1 || se.Rules[0] != "net("+host+")" {
		t.Errorf("rules = %v", se.Rules)
	}
	if r := <-fetch(); r.code != 200 || r.body != "upstream" {
		t.Errorf("after always: %+v", r)
	}
	if len(s.session(id).Asks) != 0 {
		t.Error("always still asked")
	}
}

// An always with a scope allows the pattern: the next host it covers isn't asked about.
func TestNetworkAlwaysWithAScope(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	ctx := context.Background()
	done := make(chan bool, 1)
	go func() { done <- b.egress.Allow(ctx, "pkgs.mirror.test", 443) }()
	ask := s.waitAsk(id, core.AskNetwork)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "always", Scope: "*.mirror.test"})
	if !<-done {
		t.Fatal("not allowed")
	}
	if !b.egress.Allow(ctx, "cdn.mirror.test", 443) {
		t.Error("the scope didn't cover cdn.mirror.test")
	}
	if a := sandbox.LoadAlways().Egress; len(a) != 1 || a[0].Host != "*.mirror.test" {
		t.Errorf("always = %+v", a)
	}
	if se := s.session(id); len(se.Asks) != 0 || len(se.Rules) != 1 || se.Rules[0] != "net(*.mirror.test)" {
		t.Errorf("asks %v, rules %v", se.Asks, se.Rules)
	}
}

// Layer 3's gateway: a call is held for the user; allow runs it on the host, deny returns the gateway's refusal.
func TestGatewayCallsWaitForTheUser(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	p, _ := s.session(id).Primary()
	os.WriteFile(filepath.Join(p.Dir, "new.go"), []byte("package pricing\n"), 0o644)

	call := func(tool string, args any) chan mcp.CallResult {
		ch := make(chan mcp.CallResult, 1)
		go func() { ch <- gatewayCall(t, b.root, tool, args) }()
		return ch
	}

	res := call("oriel__commit", map[string]any{"message": "Add new.go"})
	ask := s.waitAsk(id, core.AskGateway)
	if ask.Tool != "oriel__commit" || ask.Rule != "oriel__commit" || !strings.Contains(ask.Summary, "Add new.go") {
		t.Fatalf("ask = %+v", ask)
	}
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "allow"})
	if r := <-res; r.IsError || r.Text() != "Committed 1 file(s)." {
		t.Fatalf("commit = %q", r.Text())
	}

	res = call("oriel__push", map[string]any{})
	ask = s.waitAsk(id, core.AskGateway)
	select {
	case r := <-res:
		t.Fatalf("not held: %q", r.Text())
	case <-time.After(100 * time.Millisecond):
	}
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "deny", Note: "open a PR instead"})
	if r := <-res; !r.IsError || !strings.Contains(r.Text(), "the user denied oriel__push") || !strings.Contains(r.Text(), "open a PR instead") {
		t.Errorf("deny = %q, want who denied it and their note", r.Text())
	}
	if out, _ := exec.Command("git", "--git-dir", s.remote, "branch", "--list", p.Branch).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatal("a denied push pushed")
	}

	res = call("oriel__push", map[string]any{})
	ask = s.waitAsk(id, core.AskGateway)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "always"})
	if r := <-res; r.IsError || r.Text() != "Pushed." {
		t.Fatalf("push = %q", r.Text())
	}
	out, err := exec.Command("git", "--git-dir", s.remote, "log", "--format=%s", "-1", p.Branch).Output()
	if err != nil || strings.TrimSpace(string(out)) != "Add new.go" {
		t.Errorf("the remote's %s: %q %v", p.Branch, out, err)
	}
	// Always: the next push needs no card.
	if r := gatewayCall(t, b.root, "oriel__push", map[string]any{}); r.IsError {
		t.Errorf("second push = %q", r.Text())
	}
	if len(s.session(id).Asks) != 0 {
		t.Error("a ruled push still asked")
	}
}

// Layer 2 answers layer 3's asks too: the app's policy hooks, with no card.
func TestPolicyHooksAnswerSandboxAsks(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	c, _ := s.app()
	c.Conn.Handle(MethodAppDecide, func(_ context.Context, raw json.RawMessage) (any, error) {
		var d Decide
		json.Unmarshal(raw, &d)
		switch {
		case d.Ask.Kind == core.AskNetwork && d.Ask.Host == "pkgs.mirror.test:443":
			return Verdict{Decision: "allow", By: "init.lua"}, nil
		case d.Ask.Kind == core.AskNetwork && d.Ask.Host == "tracker.test:443":
			return Verdict{Decision: "deny", By: "init.lua", Note: "no telemetry"}, nil
		case d.Ask.Kind == core.AskGateway && d.Ask.Tool == "oriel__push":
			return Verdict{Decision: "deny", By: "init.lua", Note: "pushes go through CI"}, nil
		}
		return Verdict{}, nil
	})
	if !b.egress.Allow(context.Background(), "pkgs.mirror.test", 443) {
		t.Error("the hook's allow didn't let the connection through")
	}
	if b.egress.Allow(context.Background(), "tracker.test", 443) {
		t.Error("the hook's deny let the connection through")
	}
	if why := b.egress.Why("tracker.test", 443); !strings.Contains(why, "init.lua") || !strings.Contains(why, "no telemetry") {
		t.Errorf("the refusal's reason %q doesn't name the hook and its note", why)
	}
	if r := gatewayCall(t, b.root, "oriel__push", map[string]any{}); !r.IsError || !strings.Contains(r.Text(), "init.lua denied oriel__push") ||
		!strings.Contains(r.Text(), "pushes go through CI") {
		t.Errorf("push = %q, want who denied it and why", r.Text())
	}
	if len(s.session(id).Asks) != 0 {
		t.Error("a hook's answer still showed a card")
	}
}

func TestRequestHostRefusalHolds(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	res := make(chan mcp.CallResult, 1)
	go func() { res <- gatewayCall(t, b.root, "oriel__request_host", map[string]any{"why": "needs docker"}) }()
	ask := s.waitAsk(id, core.AskGateway)
	if ask.Tool != "oriel__request_host" || !strings.Contains(ask.Summary, "needs docker") {
		t.Fatalf("ask = %+v", ask)
	}
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "deny"})
	if r := <-res; !r.IsError || !strings.Contains(r.Text(), "refused") {
		t.Errorf("deny = %q", r.Text())
	}
	if r := gatewayCall(t, b.root, "oriel__request_host", map[string]any{"why": "again"}); !r.IsError || !strings.Contains(r.Text(), "already refused") {
		t.Errorf("again = %q", r.Text())
	}
}

func TestReposListsWhatCouldJoin(t *testing.T) {
	s := newSandboxRig(t)
	_, _, b := s.sandboxed()
	other := t.TempDir()
	exec.Command("git", "init", "-q", other).Run()
	if _, err := s.w.repos.add(context.Background(), s.w.opts.ID, other); err != nil {
		t.Fatal(err)
	}
	r := gatewayCall(t, b.root, "oriel__repos", map[string]any{})
	if r.IsError || !strings.Contains(r.Text(), other) || strings.Contains(r.Text(), s.repo.Dir+"\n") {
		t.Errorf("repos = %q", r.Text())
	}
}

// agents.md, "Sandbox settings": the app sends the sandbox.* options as a sandbox.Config; the worker keeps it across
// a restart, and a session's sandbox runs on it, with its repos' own rules.
func TestSandboxRunsOnTheConfigTheAppSent(t *testing.T) {
	s := newSandboxRig(t)
	c, _ := s.app()
	cfg := sandbox.Config{Memory: "2g", PidsLimit: 512, ApprovalSecs: 90, Env: []string{"GOFLAGS"},
		Egress:   []sandbox.EgressRule{{Host: "proxy.golang.org", Allow: true}},
		Rules:    []sandbox.PolicyRule{{Tool: "oriel__push", Action: "deny"}},
		Projects: map[string]sandbox.ProjectConfig{s.repo.Dir: {Egress: []sandbox.EgressRule{{Host: "registry.npmjs.org", Port: 443, Allow: true}}}}}
	if err := c.SetSandboxConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	s.restart()
	s.f.Attach(s.w)
	s.w.opts.Sandbox = s.f
	_, spec, b := s.sandboxed()
	if b.cfg.Memory != "2g" || b.cfg.PidsLimit != 512 || b.gateway.Timeout != 90*time.Second || len(b.cfg.Env) != 1 {
		t.Errorf("the sandbox runs on %+v", b.cfg)
	}
	p, err := spec.Exec.Start(context.Background(), exec.Command("claude"))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(p.Stdout())
	p.Wait()
	if o := <-s.opts; o.Config.Memory != "2g" || o.Config.PidsLimit != 512 {
		t.Errorf("the container launches with %+v", o.Config)
	}
	var hosts []string
	for _, layer := range b.egressSettings() {
		for _, r := range layer {
			hosts = append(hosts, r.Host)
		}
	}
	if !slices.Contains(hosts, "proxy.golang.org") || !slices.Contains(hosts, "registry.npmjs.org") {
		t.Errorf("the egress rules are %v", hosts)
	}
	if l := b.gatewayLevels(); !slices.ContainsFunc(l.Global, func(r gateway.Rule) bool { return r.Tool == "oriel__push" && r.Action == "deny" }) {
		t.Errorf("the gateway's global rules are %+v", l.Global)
	}
}

// agents.md, "Sandbox settings": a worker elsewhere reads its repos' own .oriel/settings.toml, trusted as the app
// trusts one (by its SHA-256, again after a change), and merges it under the values the app sent; until it's
// trusted a notice offers to.
func TestWorkerReadsItsReposProjectFiles(t *testing.T) {
	s := newSandboxRig(t)
	ctx := context.Background()
	c, store := s.app()
	file := filepath.Join(s.repo.Dir, ".oriel", "settings.toml")
	s.repo.Write(map[string]string{".oriel/settings.toml": "[sandbox]\negress = [\"allow registry.npmjs.org:443\"]\nrules = [\"deny oriel__push\"]\n"})
	cfg := sandbox.Config{Memory: "2g", ReadProjects: true, Egress: []sandbox.EgressRule{{Host: "proxy.golang.org", Allow: true}}}
	if err := c.SetSandboxConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	_, _, b := s.sandboxed()
	if _, ok := b.cfg.Projects[s.repo.Dir]; ok {
		t.Errorf("an untrusted project file applied: %+v", b.cfg.Projects)
	}
	var trust core.Action
	for deadline := time.Now().Add(5 * time.Second); trust.Command == ""; time.Sleep(10 * time.Millisecond) {
		for _, n := range store.State().Notifications {
			if len(n.Actions) > 0 && n.Actions[0].Command == "settings.trust" {
				trust = n.Actions[0]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notice offering to trust %s: %+v", file, store.State().Notifications)
		}
	}
	if trust.Args["path"] != file || trust.Args["worker"] != string(s.w.opts.ID) {
		t.Errorf("the notice's Trust runs %+v", trust)
	}
	if err := c.TrustSettings(ctx, file); err != nil {
		t.Fatal(err)
	}
	_, _, b = s.sandboxed()
	p := b.cfg.Projects[s.repo.Dir]
	if b.cfg.Memory != "2g" || len(p.Egress) != 1 || p.Egress[0].Host != "registry.npmjs.org" || len(p.Rules) != 1 {
		t.Errorf("the sandbox runs on %+v", b.cfg)
	}
	var hosts []string
	for _, layer := range b.egressSettings() {
		for _, r := range layer {
			hosts = append(hosts, r.Host)
		}
	}
	if !slices.Contains(hosts, "proxy.golang.org") || !slices.Contains(hosts, "registry.npmjs.org") {
		t.Errorf("the egress rules are %v", hosts)
	}
	if got := s.w.SandboxConfig(); got.Projects != nil {
		t.Errorf("reading the project file changed what the app sent: %+v", got.Projects)
	}

	s.repo.Write(map[string]string{".oriel/settings.toml": "[sandbox]\nimage = \"evil\"\n"})
	_, _, b = s.sandboxed()
	if p, ok := b.cfg.Projects[s.repo.Dir]; ok {
		t.Errorf("a changed file applied without trusting it again: %+v", p)
	}
	if err := c.TrustSettings(ctx, filepath.Join(t.TempDir(), "settings.toml")); err == nil {
		t.Error("trusted a file that isn't a registered repo's settings")
	}
}

// A worker the app can see the files of (this machine's) leaves the project layer to the app.
func TestWorkerLeavesProjectsToTheApp(t *testing.T) {
	s := newSandboxRig(t)
	c, _ := s.app()
	s.repo.Write(map[string]string{".oriel/settings.toml": "[sandbox]\nimage = \"x\"\n"})
	if _, err := s.w.repos.add(context.Background(), s.w.opts.ID, s.repo.Dir); err != nil {
		t.Fatal(err)
	}
	if err := c.TrustSettings(context.Background(), filepath.Join(s.repo.Dir, ".oriel", "settings.toml")); err != nil {
		t.Fatal(err)
	}
	_, _, b := s.sandboxed()
	if len(b.cfg.Projects) != 0 {
		t.Errorf("without ReadProjects the worker read %+v", b.cfg.Projects)
	}
}

// settings.md, "sandbox.servers": a server the app sent reaches the gateway beside the ones Claude Code has.
func TestGatewaySeesConfiguredServers(t *testing.T) {
	s := newSandboxRig(t)
	c, _ := s.app()
	cfg := sandbox.Config{Servers: map[string]json.RawMessage{}}
	for name, line := range map[string]string{"docs": "https://docs.example.com/mcp", "linear": "npx -y linear-mcp --flag"} {
		raw, err := sandbox.ParseServer(line)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Servers[name] = raw
	}
	cfg.Hidden = []string{"linear"}
	if err := c.SetSandboxConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	_, _, b := s.sandboxed()
	var got []string
	for _, sv := range b.gateway.Up.Servers {
		got = append(got, sv.Name+" "+sv.Type+" "+sv.URL+sv.Command+" "+strings.Join(sv.Args, " "))
	}
	if !slices.Contains(got, "docs http https://docs.example.com/mcp ") || slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "linear") }) {
		t.Errorf("the gateway's upstreams are %q", got)
	}
	cfg.Hidden = nil
	c.SetSandboxConfig(context.Background(), cfg)
	_, _, b = s.sandboxed()
	got = nil
	for _, sv := range b.gateway.Up.Servers {
		got = append(got, sv.Name+" "+sv.Type+" "+sv.URL+sv.Command+" "+strings.Join(sv.Args, " "))
	}
	if !slices.Contains(got, "linear stdio npx -y linear-mcp --flag") {
		t.Errorf("the gateway's upstreams are %q", got)
	}
}
