package claude

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// connectorHarness is the fake's harness with spec changed.
func connectorHarness(t *testing.T, change func(*agent.StartSpec)) agenttest.Harness {
	h := fakeHarness(t)
	spec := h.Spec
	h.Spec = func(t *testing.T, dir string) agent.StartSpec {
		s := spec(t, dir)
		change(&s)
		return s
	}
	return h
}

// Every MCP call asks Oriel, whatever Claude's own mode would do with it: a connector's call raises Oriel's
// permission, which the user answers, on the host's gate and in a sandbox's bypassPermissions alike.
func TestAConnectorCallAsksOriel(t *testing.T) {
	for name, change := range map[string]func(*agent.StartSpec){
		"asking":    func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk },
		"light":     func(s *agent.StartSpec) { s.Policy = agent.PolicyLight },
		"sandboxed": func(s *agent.StartSpec) { s.Sandbox = &agent.Sandbox{Name: "box"}; s.Policy = agent.PolicyBypass },
	} {
		t.Run(name, func(t *testing.T) {
			s := agenttest.Start(t, connectorHarness(t, change))
			s.Send("call a connector")
			p := s.Wait("the permission", func(ev agent.Event) bool { _, ok := ev.(agent.Permission); return ok }).(agent.Permission)
			if p.Tool != fakeConnector || p.Rule != fakeConnector {
				t.Errorf("permission = %+v", p)
			}
			if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Allow: true}); err != nil {
				t.Fatal(err)
			}
			if got := s.Reply(); !strings.Contains(got, "ran: the index") {
				t.Errorf("allowed, the reply is %q", got)
			}
			s.Send("call a connector")
			p = s.Wait("the second permission", func(ev agent.Event) bool { q, ok := ev.(agent.Permission); return ok && q.ID != p.ID }).(agent.Permission)
			if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Note: "not now"}); err != nil {
				t.Fatal(err)
			}
			if got := s.Reply(); !strings.Contains(got, "denied: Denied by the user: not now") {
				t.Errorf("denied, the reply is %q", got)
			}
		})
	}
}

// Oriel's own tools never ask: they run without a permission, whatever the gate.
func TestOrielsOwnToolsNeverAsk(t *testing.T) {
	s := agenttest.Start(t, connectorHarness(t, func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk }))
	s.Send("call echo")
	if got := s.Reply(); !strings.Contains(got, "echo: hi") {
		t.Errorf("reply = %q", got)
	}
	for _, ev := range s.Events {
		if p, ok := ev.(agent.Permission); ok {
			t.Errorf("Oriel's own tool asked: %+v", p)
		}
	}
}

// A utility agent has nobody to ask: Oriel answers its MCP calls in the check, at once. What it was allowed runs,
// and the rest is refused with a reason, neither raising a permission.
func TestAUtilityAgentsMCPCallsAreAnsweredAtOnce(t *testing.T) {
	utility := func(allow ...string) func(*agent.StartSpec) {
		return func(s *agent.StartSpec) { s.Policy, s.Unattended, s.Allow = agent.PolicyRefuse, true, allow }
	}
	s := agenttest.Start(t, connectorHarness(t, utility("WebSearch")))
	s.Send("call a connector")
	if got := s.Reply(); !strings.Contains(got, "denied: Oriel: nobody is here to approve "+fakeConnector) {
		t.Errorf("outside its Allow, the reply is %q", got)
	}
	s.Send("call echo")
	if got := s.Reply(); !strings.Contains(got, "echo: hi") {
		t.Errorf("its own tool: %q", got)
	}
	a := agenttest.Start(t, connectorHarness(t, utility(fakeConnector)))
	a.Send("call a connector")
	if got := a.Reply(); !strings.Contains(got, "ran: the index") {
		t.Errorf("in its Allow, the reply is %q", got)
	}
	for _, ev := range slices.Concat(s.Events, a.Events) {
		if p, ok := ev.(agent.Permission); ok {
			t.Errorf("a utility agent asked: %+v", p)
		}
	}
}

// A sandboxed session keeps claude.ai's connectors: their calls ask Oriel like any MCP call, so nothing needs them
// turned off. A session started without MCP servers still has none.
func TestASandboxKeepsClaudeAIConnectors(t *testing.T) {
	const off = "ENABLE_CLAUDEAI_MCP_SERVERS=false"
	if env := processEnv(agent.StartSpec{Sandbox: &agent.Sandbox{Name: "box"}}, "/s", "/oriel"); slices.Contains(env, off) {
		t.Errorf("a sandbox turned the connectors off: %q", env)
	}
	if env := processEnv(agent.StartSpec{NoMCP: true}, "/s", "/oriel"); !slices.Contains(env, off) {
		t.Errorf("a session without MCP servers kept the connectors: %q", env)
	}
}
