package sandboxhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// fakeInstalled is a provider with MCP servers installed for it; only those are asked of it.
type fakeInstalled struct {
	agent.Provider
	servers []agent.MCPServer
}

func (p fakeInstalled) SandboxNeeds(context.Context, agent.SandboxPlace) (agent.SandboxNeeds, error) {
	return agent.SandboxNeeds{}, nil
}

func (p fakeInstalled) MCPServers() []agent.MCPServer { return p.servers }

// A sandbox's agent reaches the servers its gateway carries: Oriel's own (sandbox.servers) and each provider's,
// less the hidden ones; agent.Covered finds a tool's server among them.
func TestMCPServersAreTheGatewaysOwn(t *testing.T) {
	x, err := New(Deps{Env: hostcore.Env{Home: t.TempDir()}, Providers: func() []agent.Provider {
		return []agent.Provider{fakeInstalled{servers: []agent.MCPServer{
			{Name: "lin", Config: json.RawMessage(`{"command":"npx","args":["mcp-remote","https://mcp.linear.app/sse"]}`)},
			{Name: "secret", Config: json.RawMessage(`{"type":"http","url":"https://secret.example.com/mcp"}`)},
		}}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := x.SetConfig(sandbox.Config{Hidden: []string{"secret"},
		Servers: map[string]json.RawMessage{"docs": json.RawMessage(`{"type":"http","url":"https://docs.example.com/mcp"}`)}}); err != nil {
		t.Fatal(err)
	}
	got := x.MCPServers()
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "docs" || names[1] != "lin" {
		t.Errorf("servers %v, want docs (Oriel's own) then lin; secret is hidden", names)
	}
	if !agent.Covered(got, []string{"mcp.linear.app"}) || agent.Covered(got, []string{"secret.example.com"}) {
		t.Error("a tool's cover isn't found among the gateway's servers as they are")
	}
}
