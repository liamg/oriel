package host

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
)

// boxed is a provider that says what its agent needs in a sandbox.
type boxed struct {
	*agenttest.Fake
	needs   agent.SandboxNeeds
	servers []agent.MCPServer
	at      chan agent.SandboxPlace
}

func (b *boxed) SandboxNeeds(_ context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	b.at <- at
	return b.needs, nil
}

func (b *boxed) MCPServers() []agent.MCPServer { return b.servers }

// A sandboxed session's provider is asked what its agent needs, at its own directory in the sandbox: the hosts it
// reaches are allowed without a card, the MCP servers the user installed for it are behind the gateway, and the
// launch gets the rest.
func TestTheSandboxGivesTheProviderWhatItNeeds(t *testing.T) {
	p := &boxed{Fake: agenttest.NewFake(), at: make(chan agent.SandboxPlace, 4),
		needs:   agent.SandboxNeeds{Hosts: []string{"api.agent.example"}, Env: map[string]string{"AGENT_HOME": "x"}},
		servers: []agent.MCPServer{{Name: "notes", Source: "user", Config: json.RawMessage(`{"command":"notes-mcp"}`)}}}
	p.Name = "boxed"
	s := newSandboxRig(t, p)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "boxed", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := p.WaitStarted(t)
	b := s.keptBox(id)
	proc, err := a.Spec.Exec.Start(context.Background(), exec.Command("boxed-agent"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(proc.Stdout())
	proc.Wait()
	at := <-p.at
	if at.Dir != sandbox.AgentDir(b.Root(), "boxed") || at.StateDir != a.Spec.StateDir || at.OS != "linux" {
		t.Errorf("asked at %+v", at)
	}
	if o := <-s.opts; o.Provider != "boxed" {
		t.Errorf("launched for %q", o.Provider)
	}
	if !b.Egress().Allow(context.Background(), "api.agent.example", 443) {
		t.Error("the provider's host needs a card")
	}
	var names []string
	for _, u := range b.Gateway().Up.Servers {
		names = append(names, u.Name)
	}
	if !slices.Contains(names, "notes") {
		t.Errorf("the gateway's servers = %v", names)
	}
}

// Every sandbox's gateway carries the servers installed for every provider, not just its own agent's, and a
// sandbox.servers entry wins over an installed server of its name.
func TestTheGatewayCarriesEveryProvidersServers(t *testing.T) {
	other := &boxed{Fake: agenttest.NewFake(), at: make(chan agent.SandboxPlace, 4),
		servers: []agent.MCPServer{{Name: "browser", Source: "user", Config: json.RawMessage(`{"command":"browser-mcp"}`)},
			{Name: "docs", Source: "user", Config: json.RawMessage(`{"command":"installed-docs"}`)}}}
	other.Name = "other"
	s := newSandboxRig(t, other)
	raw, err := sandbox.ParseServer("https://docs.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.w.SetSandboxConfig(sandbox.Config{Servers: map[string]json.RawMessage{"docs": raw}}); err != nil {
		t.Fatal(err)
	}
	_, _, b := s.sandboxed()
	got := map[string]string{}
	for _, u := range b.Gateway().Up.Servers {
		got[u.Name] = u.Source + " " + u.Command + u.URL
	}
	if got["browser"] != "user browser-mcp" {
		t.Errorf("a session on the fake provider lacks the other provider's server: %v", got)
	}
	if got["docs"] != "oriel https://docs.example.com/mcp" {
		t.Errorf("sandbox.servers' docs should win the clash: %v", got)
	}
}
