package agentui_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
)

type testBackend = rig.EchoBackend

// sandboxedSession starts a sandboxed session with the fake agent, on a host whose sandboxes run testBackend.
func sandboxedSession(t *testing.T) (*apptest.Window, *rig.Host, core.SessionState) {
	t.Helper()
	return sandboxedSessionOn(t, testBackend{})
}

// sandboxedSessionOn is sandboxedSession on a host whose sandboxes run backend.
func sandboxedSessionOn(t *testing.T, backend sandbox.Backend) (*apptest.Window, *rig.Host, core.SessionState) {
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
	lw := rig.NewHostWith(t, host.Options{Sandbox: f, Home: home})
	f.Attach(lw.W)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err = r.Do(func() error {
		return r.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo,
			"sandbox": true, "network": "ask"})
	})
	if err != nil {
		t.Fatal(err)
	}
	lw.Fake.WaitStarted(t)
	st := r.UntilState("the sandboxed session in front", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && st.Focus == "w" && se.Snapshot().Agent.Sandboxed
	})
	se, _ := st.Session("w")
	return r, lw, se.Snapshot()
}

// The Permissions view lists the rules in force with their sources (an endpoint the sandbox's environment points at among them), sets the
// session's own, and shows the connections the sandbox reported, each of which can be blocked from its row.
func TestPermissionsViewShowsRulesAndConnections(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://litellm.tail1234.ts.net:4000")
	r, lw, se := sandboxedSession(t)
	r.TypeKeys("<D-A>")
	r.ClickText("Permissions and connections ›")
	r.WaitFrame("the rules", func(s string) bool {
		return apptest.HasAll(s, "TOOLS", "NETWORK", "litellm.tail1234.ts.net:4000", "the sandbox's environment · private addresses too",
			"hosts the agent needs", "built-in list", "anything else", "TOOLS")
	})
	r.ClickText("Show")
	r.WaitText("github.com:443")
	r.ClickText("Hide")
	r.WaitGone("github.com:443")

	r.ClickText("host or host:port, *.example.com")
	r.TypeText("tracker.example.com:443")
	r.ClickText("Block for session")
	r.WaitFrame("the session's block", func(s string) bool { return apptest.HasAll(s, "tracker.example.com:443", "Remove") })
	p, err := lw.W.Permissions(context.Background(), se.ID)
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
	r.ClickText("CONNECTIONS")
	r.WaitFrame("the connection", func(s string) bool {
		return apptest.HasAll(s, "registry.npmjs.org:443", "ALLOWED", "1 connection", "↑2.0 KB ↓4.0 KB")
	})
	r.ClickText("Block")
	apptest.Poll(t, "the row's Block to make a rule", func() bool {
		p, _ := lw.W.Permissions(context.Background(), se.ID)
		return slices.ContainsFunc(p.Hosts, func(r egress.Rule) bool { return r.Host == "registry.npmjs.org" && r.Port == 443 && !r.Allow })
	})
}
