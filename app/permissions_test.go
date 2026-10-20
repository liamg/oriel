package app

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
)

// testBackend runs an echo in place of the agent's sandbox: everything else of the sandbox (its socket, gateway and
// egress session) is real.
type testBackend struct{}

func (testBackend) Name() string { return "test" }
func (testBackend) Launch(context.Context, string, string, []string, sandbox.LaunchOptions) (sandbox.Command, error) {
	return sandbox.Command{Argv: []string{"echo", "in the sandbox"}, Env: os.Environ()}, nil
}
func (testBackend) Available() error                                         { return nil }
func (testBackend) Stop(context.Context, string) error                       { return nil }
func (testBackend) Health(context.Context, sandbox.Config) []sandbox.Problem { return nil }
func (testBackend) Sweep(context.Context, func(string) bool) []string        { return nil }
func (testBackend) AgentRunDir(string) string                                { return "/run/oriel" }
func (testBackend) ServesPort() bool                                         { return false }

// sandboxedSession starts a sandboxed session with the fake agent, on a host whose sandboxes run testBackend.
func sandboxedSession(t *testing.T) (*running, *localHost, core.SessionState) {
	t.Helper()
	return sandboxedSessionOn(t, testBackend{})
}

// sandboxedSessionOn is sandboxedSession on a host whose sandboxes run backend.
func sandboxedSessionOn(t *testing.T, backend sandbox.Backend) (*running, *localHost, core.SessionState) {
	t.Helper()
	// The session socket's path must fit the unix limit: a short home.
	home, err := os.MkdirTemp("/tmp", "op")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	f := host.NewSandboxFactory(func() sandbox.Config { return sandbox.Config{} })
	f.Backend = func(sandbox.Config) (sandbox.Backend, error) { return backend, nil }
	f.Available = func() []string { return []string{"test"} }
	lw := newHostWith(t, host.Options{Sandbox: f, Home: home})
	f.Attach(lw.w)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err = r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo,
			"sandbox": true, "network": "ask"})
	})
	if err != nil {
		t.Fatal(err)
	}
	lw.fake.WaitStarted(t)
	st := waitState(t, r.store, "the sandboxed session in front", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && st.Focus == "w" && se.Snapshot().Agent.Sandboxed
	})
	se, _ := st.Session("w")
	return r, lw, se.Snapshot()
}

// The Permissions view lists the rules in force with their sources (the model's endpoint among them), sets the
// session's own, and shows the connections the sandbox reported, each of which can be blocked from its row.
func TestPermissionsViewShowsRulesAndConnections(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "http://litellm.tail1234.ts.net:4000")
	r, lw, se := sandboxedSession(t)
	r.typeKeys("<D-A>")
	r.clickText("Permissions and connections ›")
	r.waitFrame("the rules", func(s string) bool {
		return containsAll(s, "TOOLS", "NETWORK", "litellm.tail1234.ts.net:4000", "the sandbox's environment · private addresses too",
			"hosts the agent needs", "built-in list", "anything else", "TOOLS")
	})
	r.clickText("Show")
	r.waitText("api.anthropic.com:443")
	r.clickText("Hide")
	r.waitGone("api.anthropic.com:443")

	r.clickText("host or host:port, *.example.com")
	r.typeText("tracker.example.com:443")
	r.clickText("Block for session")
	r.waitFrame("the session's block", func(s string) bool { return containsAll(s, "tracker.example.com:443", "Remove") })
	p, err := lw.w.Permissions(context.Background(), se.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(p.Hosts, func(r egress.Rule) bool {
		return r.Host == "tracker.example.com" && r.Port == 443 && !r.Allow && r.Source == "this session"
	}) {
		t.Fatalf("hosts = %+v", p.Hosts)
	}

	// A connection the sandbox's proxy reports over the session socket, as it does from the container.
	root := se.Dir
	if p, ok := se.Primary(); root == "" && ok {
		root = p.Dir
	}
	rec, _ := sandbox.Load(root)
	c := agentsock.NewSocket(sandbox.SocketPath(root), rec.AgentToken)
	if err := c.EgressLog(egress.Event{Host: "registry.npmjs.org", Port: 443, Allowed: true, Proto: "tls", At: time.Now(), BytesUp: 2048, BytesDown: 4096}); err != nil {
		t.Fatal(err)
	}
	r.clickText("CONNECTIONS")
	r.waitFrame("the connection", func(s string) bool {
		return containsAll(s, "registry.npmjs.org:443", "ALLOWED", "1 connection", "↑2.0 KB ↓4.0 KB")
	})
	r.clickText("Block")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		p, _ := lw.w.Permissions(context.Background(), se.ID)
		if slices.ContainsFunc(p.Hosts, func(r egress.Rule) bool { return r.Host == "registry.npmjs.org" && r.Port == 443 && !r.Allow }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row's Block never made a rule: %+v", p.Hosts)
		}
	}
}
