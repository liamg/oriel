package host

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
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sandboxhost"
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
	hold     chan struct{} // when set, a launch reports a step and waits on it
	swept    chan bool     // when set, each sweep sends whether the session's sandbox was attached
	root     string        // the root swept asks about
}

// fakeBackend launches an echo in place of the agent.
type fakeBackend struct{ s *sandboxRig }

func (fakeBackend) Name() string { return "fake" }

func (f fakeBackend) Launch(ctx context.Context, root, cwd string, argv []string, o sandbox.LaunchOptions) (sandbox.Command, error) {
	f.s.launched <- argv
	f.s.opts <- o
	if f.s.hold != nil {
		sandbox.Report(ctx, sandbox.Progress{Step: "Building the sandbox image", Note: "Only the first time"})
		<-f.s.hold
	}
	needs, err := sandbox.Needs(ctx, root, cwd, o, agent.SandboxPlace{OS: "linux", Arch: "amd64", Home: "/home/agent"})
	if err != nil {
		return sandbox.Command{}, err
	}
	return sandbox.Command{Argv: []string{"sh", "-c", "echo in the container; echo its stderr >&2"}, Env: os.Environ(), Needs: needs}, nil
}

func (fakeBackend) Available() error { return nil }

func (fakeBackend) Stop(context.Context, string) error { return nil }

func (fakeBackend) Health(context.Context, sandbox.Config) []sandbox.Problem { return nil }

func (f fakeBackend) Sweep(_ context.Context, attached func(string) bool) []string {
	if f.s.swept != nil {
		f.s.swept <- attached(f.s.root)
	}
	return nil
}

func (fakeBackend) AgentRunDir(string) string { return "/run/oriel" }

func (fakeBackend) ServesPort() bool { return false }

func newSandboxRig(t *testing.T, more ...agent.Provider) *sandboxRig {
	t.Helper()
	// The session socket lives under $HOME; a short one keeps its path under the unix limit.
	home, err := os.MkdirTemp("/tmp", "oh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	r := newRig(t, more...)
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

func (s *sandboxRig) sandboxed() (core.SessionID, *agent.StartSpec, *sandboxhost.Box) {
	s.t.Helper()
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	return id, &a.Spec, s.keptBox(id)
}

// keptBox is the sandbox a session keeps for its agent, once start has kept it: the fake agent starts inside launch,
// before then.
func (r *rig) keptBox(id core.SessionID) *sandboxhost.Box {
	r.t.Helper()
	until(r.t, "the session to keep its sandbox", func() bool { return r.w.boxOf(id) != nil })
	return r.w.boxOf(id)
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

// A session runs in the backend it was started with, one of those the host offers, over sandbox.backend.
func TestSandboxBackendPerSession(t *testing.T) {
	s := newSandboxRig(t)
	s.f.Available = func() []string { return []string{"docker", "seatbelt"} }
	chose := make(chan string, 4)
	s.f.Backend = func(cfg sandbox.Config) (sandbox.Backend, error) { chose <- cfg.Backend; return fakeBackend{s}, nil }
	if got := s.w.machineState().Sandboxes; !slices.Equal(got, []string{"docker", "seatbelt"}) {
		t.Errorf("the host offers %v", got)
	}
	spec := core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask", Backend: "podman"}}
	if _, err := s.w.Start(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "can't run a podman sandbox") {
		t.Errorf("a backend the host lacks: %v", err)
	}
	spec.Sandbox.Backend = "seatbelt"
	id := s.start(spec)
	select {
	case got := <-chose:
		if got != "seatbelt" {
			t.Errorf("the session runs in %q", got)
		}
	case <-time.After(waitTimeout):
		t.Fatal("no sandbox opened")
	}
	if se, _ := s.w.session(id); se.Agent.Backend != "seatbelt" {
		t.Errorf("the session keeps %q", se.Agent.Backend)
	}
}

func TestSandboxSpecNamesTheGateway(t *testing.T) {
	s := newSandboxRig(t)
	_, spec, b := s.sandboxed()
	if spec.Sandbox == nil || spec.Sandbox.Name != "fake:"+sandbox.Key(b.Root()) {
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
	if !sandbox.Enabled(b.Root()) {
		t.Error("the workspace has no sandbox record")
	}
	if m, _ := sandbox.LoadManifest(b.Root()); len(m.Projects) != 1 || filepath.IsAbs(m.Projects[0].Dir) {
		t.Errorf("manifest = %+v", m)
	}
}

// The provider's command runs in the container: runner.Launch gets the agent's argv, its provider and the env the
// provider added, and the docker command it returns is what runs, its stderr the provider's.
func TestSandboxExecRunsTheLaunchCommand(t *testing.T) {
	s := newSandboxRig(t)
	_, spec, _ := s.sandboxed()
	cmd := exec.Command("claude", "--plugin-dir", "/state/claude-mod", "--permission-mode", "bypassPermissions")
	cmd.Env = append(os.Environ(), "ORIEL_HOOK_SOCK=/tmp/oriel-1/hook.sock", "ORIEL_BIN=/home/me/bin/oriel")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	p, err := spec.Exec.Start(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(p.Stdout())
	p.Wait()
	if strings.TrimSpace(string(out)) != "in the container" || strings.TrimSpace(stderr.String()) != "its stderr" {
		t.Errorf("ran %q, stderr %q", out, stderr.String())
	}
	argv, o := <-s.launched, <-s.opts
	if argv[0] != "claude" || o.Provider != "fake" || o.StateDir != spec.StateDir || o.StateDir == "" {
		t.Errorf("argv %v, provider %q, state %q", argv, o.Provider, o.StateDir)
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
	mounted, err := net.Dial("unix", filepath.Join(sandbox.RunDir(b.Root()), "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip("the run dir's socket", mounted)
	rec, _ := sandbox.Load(b.Root())
	raw, err := net.Dial("unix", sandbox.SocketPath(b.Root()))
	if err != nil {
		t.Fatal(err)
	}
	relayed, err := egress.Upgrade(context.Background(), raw, rec.AgentToken, agentsock.HookPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip("the relay's route", relayed)
}

// Permissions, layer 3: an unknown host's connection is held while the user decides.
func TestNetworkAsksHoldTheConnection(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "upstream") }))
	defer up.Close()
	proxy := httptest.NewServer(&egress.Proxy{Decider: b.Egress()})
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
	default:
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

// A connection that never got through shows in the transcript, once per destination, without the agent's terminal.
func TestNetworkFailuresReachTheTranscript(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	proxy := httptest.NewServer(&egress.Proxy{Decider: b.Egress(), Log: b.Egress().Record})
	defer proxy.Close()
	paddr := strings.TrimPrefix(proxy.URL, "http://")
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := strings.TrimPrefix(up.URL, "http://")
	up.Close()

	for range 2 {
		res := make(chan int, 1)
		go func() { code, _ := get(t, paddr, up.URL); res <- code }()
		ask := s.waitAsk(id, core.AskNetwork)
		s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "deny", Note: "not today"})
		if code := <-res; code != http.StatusForbidden {
			t.Fatalf("denied: %d", code)
		}
	}
	se := s.eventually(id, "the refusal in the transcript", func(se core.SessionState) bool { return len(se.Transcript) > 0 })
	var notes []core.Item
	for _, it := range se.Transcript {
		if strings.HasPrefix(it.Text, "Network:") {
			notes = append(notes, it)
		}
	}
	if len(notes) != 1 || notes[0].Text != "Network: blocked "+host+" (denied by the user, who said: not today)" {
		t.Fatalf("notes = %+v", notes)
	}

	// Allowed but unreachable: an error, for a destination not shown yet.
	if err := b.Egress().SetRule("127.0.0.1", 1, true, false); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, paddr, "http://127.0.0.1:1"); code != http.StatusBadGateway {
		t.Fatalf("unreachable: %d", code)
	}
	s.eventually(id, "the dial failure in the transcript", func(se core.SessionState) bool {
		for _, it := range se.Transcript {
			if it.Kind == core.ItemError && strings.HasPrefix(it.Text, "Network: couldn't reach 127.0.0.1:1: ") {
				return true
			}
		}
		return false
	})
}

// An always with a scope allows the pattern: the next host it covers isn't asked about.
func TestNetworkAlwaysWithAScope(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	ctx := context.Background()
	done := make(chan bool, 1)
	go func() { done <- b.Egress().Allow(ctx, "pkgs.mirror.test", 443) }()
	ask := s.waitAsk(id, core.AskNetwork)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "always", Scope: "*.mirror.test"})
	if !<-done {
		t.Fatal("not allowed")
	}
	if !b.Egress().Allow(ctx, "cdn.mirror.test", 443) {
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
		go func() { ch <- gatewayCall(t, b.Root(), tool, args) }()
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
	default:
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
	if r := gatewayCall(t, b.Root(), "oriel__push", map[string]any{}); r.IsError {
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
	s.w.SetApp(App{Decide: func(_ context.Context, d Decide) Verdict {
		switch {
		case d.Ask.Kind == core.AskNetwork && d.Ask.Host == "pkgs.mirror.test:443":
			return Verdict{Decision: "allow", By: "init.lua"}
		case d.Ask.Kind == core.AskNetwork && d.Ask.Host == "tracker.test:443":
			return Verdict{Decision: "deny", By: "init.lua", Note: "no telemetry"}
		case d.Ask.Kind == core.AskGateway && d.Ask.Tool == "oriel__push":
			return Verdict{Decision: "deny", By: "init.lua", Note: "pushes go through CI"}
		}
		return Verdict{}
	}})
	if !b.Egress().Allow(context.Background(), "pkgs.mirror.test", 443) {
		t.Error("the hook's allow didn't let the connection through")
	}
	if b.Egress().Allow(context.Background(), "tracker.test", 443) {
		t.Error("the hook's deny let the connection through")
	}
	if why := b.Egress().Why("tracker.test", 443); !strings.Contains(why, "init.lua") || !strings.Contains(why, "no telemetry") {
		t.Errorf("the refusal's reason %q doesn't name the hook and its note", why)
	}
	if r := gatewayCall(t, b.Root(), "oriel__push", map[string]any{}); !r.IsError || !strings.Contains(r.Text(), "init.lua denied oriel__push") ||
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
	go func() { res <- gatewayCall(t, b.Root(), "oriel__request_host", map[string]any{"why": "needs docker"}) }()
	ask := s.waitAsk(id, core.AskGateway)
	if ask.Tool != "oriel__request_host" || !strings.Contains(ask.Summary, "needs docker") {
		t.Fatalf("ask = %+v", ask)
	}
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "deny"})
	if r := <-res; !r.IsError || !strings.Contains(r.Text(), "refused") {
		t.Errorf("deny = %q", r.Text())
	}
	if r := gatewayCall(t, b.Root(), "oriel__request_host", map[string]any{"why": "again"}); !r.IsError || !strings.Contains(r.Text(), "already refused") {
		t.Errorf("again = %q", r.Text())
	}
}

func TestReposListsWhatCouldJoin(t *testing.T) {
	s := newSandboxRig(t)
	_, _, b := s.sandboxed()
	other := t.TempDir()
	exec.Command("git", "init", "-q", other).Run()
	if _, err := s.w.addRepo(context.Background(), other, core.RepoAdded); err != nil {
		t.Fatal(err)
	}
	r := gatewayCall(t, b.Root(), "oriel__repos", map[string]any{})
	if r.IsError || !strings.Contains(r.Text(), other) || strings.Contains(r.Text(), s.repo.Dir+"\n") {
		t.Errorf("repos = %q", r.Text())
	}
}

// The app sends the sandbox.* options as a sandbox.Config; the host keeps it across
// a restart, and a session's sandbox runs on it, with its repos' own rules.
func TestSandboxRunsOnTheConfigTheAppSent(t *testing.T) {
	s := newSandboxRig(t)
	cfg := sandbox.Config{Memory: "2g", PidsLimit: 512, ApprovalSecs: 90, Env: []string{"GOFLAGS"},
		Egress:   []sandbox.EgressRule{{Host: "proxy.golang.org", Allow: true}},
		Rules:    []sandbox.PolicyRule{{Tool: "oriel__push", Action: "deny"}},
		Projects: map[string]sandbox.ProjectConfig{s.repo.Dir: {Egress: []sandbox.EgressRule{{Host: "registry.npmjs.org", Port: 443, Allow: true}}}}}
	if err := s.w.SetSandboxConfig(cfg); err != nil {
		t.Fatal(err)
	}
	s.restart()
	s.f.Attach(s.w)
	s.w.opts.Sandbox = s.f
	_, spec, b := s.sandboxed()
	if b.Config().Memory != "2g" || b.Config().PidsLimit != 512 || b.Gateway().Timeout != 90*time.Second || len(b.Config().Env) != 1 {
		t.Errorf("the sandbox runs on %+v", b.Config())
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
	for _, layer := range b.EgressSettings() {
		for _, r := range layer {
			hosts = append(hosts, r.Host)
		}
	}
	if !slices.Contains(hosts, "proxy.golang.org") || !slices.Contains(hosts, "registry.npmjs.org") {
		t.Errorf("the egress rules are %v", hosts)
	}
	if l := b.GatewayLevels(); !slices.ContainsFunc(l.Global, func(r gateway.Rule) bool { return r.Tool == "oriel__push" && r.Action == "deny" }) {
		t.Errorf("the gateway's global rules are %+v", l.Global)
	}
}

// sandbox.servers: a server the app sent reaches the gateway beside the ones Claude Code has.
func TestGatewaySeesConfiguredServers(t *testing.T) {
	s := newSandboxRig(t)
	cfg := sandbox.Config{Servers: map[string]json.RawMessage{}}
	for name, line := range map[string]string{"docs": "https://docs.example.com/mcp", "linear": "npx -y linear-mcp --flag"} {
		raw, err := sandbox.ParseServer(line)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Servers[name] = raw
	}
	cfg.Hidden = []string{"linear"}
	if err := s.w.SetSandboxConfig(cfg); err != nil {
		t.Fatal(err)
	}
	_, _, b := s.sandboxed()
	var got []string
	for _, sv := range b.Gateway().Up.Servers {
		got = append(got, sv.Name+" "+sv.Type+" "+sv.URL+sv.Command+" "+strings.Join(sv.Args, " "))
	}
	if !slices.Contains(got, "docs http https://docs.example.com/mcp ") || slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "linear") }) {
		t.Errorf("the gateway's upstreams are %q", got)
	}
	cfg.Hidden = nil
	s.w.SetSandboxConfig(cfg)
	_, _, b = s.sandboxed()
	got = nil
	for _, sv := range b.Gateway().Up.Servers {
		got = append(got, sv.Name+" "+sv.Type+" "+sv.URL+sv.Command+" "+strings.Join(sv.Args, " "))
	}
	if !slices.Contains(got, "linear stdio npx -y linear-mcp --flag") {
		t.Errorf("the gateway's upstreams are %q", got)
	}
}

// Starting a session doesn't wait for its agent: the session is there at once, and while the sandbox readies the
// agent's state says what it's doing (a step and why it takes a while) until the agent is up and has the prompt.
func TestStartDoesNotWaitForTheSandbox(t *testing.T) {
	s := newSandboxRig(t)
	s.fake.Launch = true
	s.hold = make(chan struct{})
	started := make(chan core.SessionID, 1)
	go func() {
		id, err := s.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake",
			Repos: []core.RepoID{core.RepoID(s.repo.Dir)}, Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
		if err != nil {
			t.Error(err)
		}
		started <- id
	}()
	var id core.SessionID
	select {
	case id = <-started:
	case <-time.After(waitTimeout):
		close(s.hold)
		t.Fatal("Start waited for the sandbox")
	}
	se := s.eventually(id, "the sandbox's step", func(se core.SessionState) bool {
		return se.Agent.Starting != nil && se.Agent.Starting.Step == "Building the sandbox image"
	})
	if se.Agent.Starting.Note != "Only the first time" || se.Agent.Starting.Since.IsZero() || se.Agent.Running {
		t.Errorf("while starting: %+v", se.Agent)
	}
	if se.Status != core.StatusWorking {
		t.Errorf("a session whose agent is starting is %s, want working", se.Status)
	}
	close(s.hold)
	a := s.agent()
	if in := a.WaitSent(t); !strings.Contains(in.Text, "Work") {
		t.Errorf("first input: %q", in.Text)
	}
	se = s.eventually(id, "the agent up", func(se core.SessionState) bool { return se.Agent.Running && se.Agent.Starting == nil })
	_ = se
}

// The Permissions view's read: the tools and network rules in force with their sources, the connections, and the
// session's own rules set from it.
func TestPermissionsListRulesAndConnections(t *testing.T) {
	s := newSandboxRig(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://litellm.tail1234.ts.net:4000")
	id, _, b := s.sandboxed()
	ctx := context.Background()

	p, err := s.w.Permissions(ctx, id)
	if err != nil || !p.Sandboxed || !p.Filtered {
		t.Fatalf("permissions = %+v, %v", p, err)
	}
	hostRule := func(p Permissions, host string, port int) (egress.Rule, bool) {
		i := slices.IndexFunc(p.Hosts, func(r egress.Rule) bool { return r.Host == host && r.Port == port })
		if i < 0 {
			return egress.Rule{}, false
		}
		return p.Hosts[i], true
	}
	if r, ok := hostRule(p, "litellm.tail1234.ts.net", 4000); !ok || !r.Allow || !r.Private || r.Source != "the sandbox's environment" {
		t.Errorf("the API endpoint = %+v, %v", r, ok)
	}
	if r, ok := hostRule(p, "github.com", 443); !ok || r.Source != "built-in list" {
		t.Errorf("github.com = %+v, %v", r, ok)
	}
	push := func(p Permissions) ToolPermission {
		i := slices.IndexFunc(p.Tools, func(tp ToolPermission) bool { return tp.Name == gateway.Prefix+"push" })
		if i < 0 {
			t.Fatalf("no push tool in %+v", p.Tools)
		}
		return p.Tools[i]
	}
	if tp := push(p); tp.Action != gateway.Ask || tp.Session != "" {
		t.Errorf("push = %+v", tp)
	}

	if err := s.w.SetToolRule(id, gateway.Prefix+"push", gateway.Deny); err != nil {
		t.Fatal(err)
	}
	if err := s.w.SetNetRule(id, "Example.COM", 443, "block", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.w.Permissions(ctx, id)
	if tp := push(p); tp.Action != gateway.Deny || tp.Source != SourceSession || tp.Session != gateway.Deny {
		t.Errorf("push after a session deny = %+v", tp)
	}
	if r, ok := hostRule(p, "example.com", 443); !ok || r.Allow || r.Source != "this session" {
		t.Errorf("example.com after a block = %+v, %v", r, ok)
	}

	if err := s.w.SetToolRule(id, gateway.Prefix+"push", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.w.SetNetRule(id, "example.com", 443, "", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.w.Permissions(ctx, id)
	if tp := push(p); tp.Session != "" || tp.Action != gateway.Ask {
		t.Errorf("push after clearing = %+v", tp)
	}
	if _, ok := hostRule(p, "example.com", 443); ok {
		t.Error("the session's block is still there")
	}

	b.Egress().Record(egress.Event{Host: "example.com", Port: 443, Proto: "tls"})
	if p, _ = s.w.Permissions(ctx, id); len(p.Conns) != 1 || p.Conns[0].Host != "example.com" {
		t.Errorf("conns = %+v", p.Conns)
	}
	if err := s.w.SetToolRule(id, gateway.Prefix+"push", "maybe"); err == nil {
		t.Error("a bad action was taken")
	}
}

// Grants made together are all kept, and a reader never sees the file half-written.
func TestGrantsTogetherAreAllKept(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	done := make(chan struct{})
	torn := make(chan string, 1)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if data, err := os.ReadFile(sandbox.GrantsPath(b.Root())); err == nil && !json.Valid(data) {
				select {
				case torn <- string(data):
				default:
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { b.Grant([]gateway.Rule{{Tool: fmt.Sprintf("tool%d", i), Action: gateway.Allow}}) })
	}
	wg.Go(func() {
		if err := s.w.SetToolRule(id, "set", gateway.Deny); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	close(done)
	if got := b.Grants(); len(got) != 21 {
		t.Errorf("%d grants kept of 21: %+v", len(got), got)
	}
	select {
	case data := <-torn:
		t.Errorf("a reader saw the grants half-written: %q", data)
	default:
	}
}

// Restarts asked for during a turn restart the agent once, when it ends; one stopped meanwhile isn't restarted.
func TestSandboxRestartsOnceWhenIdle(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	b := s.keptBox(id)
	a.Emit(agent.TurnStart{})
	s.eventually(id, "working", func(se core.SessionState) bool { return se.Agent.Working })
	if err := s.w.RestartSandbox(id); err != nil {
		t.Fatal(err)
	}
	b.RestartWhenIdle("again")
	a.Emit(agent.TurnEnd{})
	a = s.agent()
	s.noStart("twice")

	b = s.keptBox(id)
	a.Emit(agent.TurnStart{})
	s.eventually(id, "working", func(se core.SessionState) bool { return se.Agent.Working })
	b.RestartWhenIdle("again")
	if err := s.w.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	s.noStart("once stopped")
}

// A restart asked for during a turn that ends with work owed happens at the turn's end, before the agent is told to
// carry on in the old sandbox.
func TestSandboxRestartsBeforeTheCarryOn(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	b := s.keptBox(id)
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	s.eventually(id, "working", func(se core.SessionState) bool { return se.Agent.Working })
	a.Call(t, "reply", map[string]any{"thread": string(s.session(id).Threads[0].ID), "body": "Adding the project.", "working": true})
	b.RestartWhenIdle("a project joined the sandbox")
	a.Emit(agent.TurnEnd{})
	again := s.agent()
	if again == a || !a.Closed() {
		t.Error("the agent wasn't restarted")
	}
	s.noneSent(a)
}

// An "always" allow for the network that can't be saved still lets the connection through, and the transcript says
// it wasn't kept.
func TestAlwaysNotSavedReachesTheTranscript(t *testing.T) {
	s := newSandboxRig(t)
	id, _, b := s.sandboxed()
	if err := os.MkdirAll(filepath.Join(sandbox.Base(), "always.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	notSaved := func(what string) {
		t.Helper()
		s.eventually(id, what+" not saved, in the transcript", func(se core.SessionState) bool {
			for _, it := range se.Transcript {
				if it.Kind == core.ItemError && strings.Contains(it.Text, what) && strings.Contains(it.Text, "not saved") {
					return true
				}
			}
			return false
		})
	}

	done := make(chan bool, 1)
	go func() { done <- b.Egress().Allow(context.Background(), "pkgs.mirror.test", 443) }()
	ask := s.waitAsk(id, core.AskNetwork)
	s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "always"})
	if !<-done {
		t.Fatal("not allowed")
	}
	notSaved("pkgs.mirror.test")
}
