package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// PL-4: a tool is offered to the agents it's for. One for no one in particular reaches every session's agent and no
// utility agent; one for the assistant reaches only the assistant; one for work sessions only those.
func TestAgentToolsAreOfferedToTheAgentsTheyreFor(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.AgentTool{Name: "lsp_definition"})
	r.Contribute(registry.AgentTool{Name: "my_tickets", For: []string{"assistant"}})
	r.Contribute(registry.AgentTool{Name: "deploy", For: []string{"work", "job"}})
	names := func(ts []registry.AgentTool) string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Name)
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		agent   string
		utility bool
		want    string
	}{
		{"work", false, "deploy lsp_definition"},
		{"review", false, "lsp_definition"},
		{"assistant", true, "my_tickets"},
		{"other", true, ""},
	} {
		if got := names(registry.AgentToolsFor(r, c.agent, c.utility)); got != c.want {
			t.Errorf("%s: %q, want %q", c.agent, got, c.want)
		}
	}
}

// crit-reference.md, "Assistant": "Confirmation cards". An act tool runs only once the agent's owner has the user's
// yes; what it did goes to the owner, and the agent reads its result.
func TestActToolsWaitForTheOwnersYes(t *testing.T) {
	r := registry.New()
	ran := 0
	tool := registry.AgentTool{Name: "stop_session", For: []string{"assistant"},
		Confirm: func(_ registry.Ctx, p map[string]any) (registry.Confirmation, error) {
			return registry.Confirmation{Title: "Stop a session", Line: "stop " + p["id"].(string)}, nil
		},
		Run: func(registry.Ctx, map[string]any) (any, error) {
			ran++
			return registry.Acted{Result: "stopped", Line: "stopped rounding", Undo: func(registry.Ctx) error { return nil }}, nil
		}}
	ctx := registry.WithAgent(context.Background(), "assistant")
	params := map[string]any{"id": "rounding"}
	if _, err := registry.CallAgentTool(ctx, r, tool, params); err == nil || ran != 0 {
		t.Fatalf("an act ran with no one to ask: %v", err)
	}
	var asked []registry.Confirmation
	var acted []registry.Acted
	yes := false
	r.Contribute(registry.AgentOwner{Agent: "assistant",
		Confirm: func(_ registry.Ctx, name string, c registry.Confirmation) (bool, error) {
			asked = append(asked, c)
			return yes, nil
		},
		Acted: func(_ registry.Ctx, name string, a registry.Acted) { acted = append(acted, a) }})
	if _, err := registry.CallAgentTool(ctx, r, tool, params); !errors.Is(err, registry.ErrNotConfirmed) || ran != 0 {
		t.Fatalf("a no ran it: %v", err)
	}
	yes = true
	out, err := registry.CallAgentTool(ctx, r, tool, params)
	if err != nil || out != "stopped" || ran != 1 {
		t.Fatalf("a yes: %q %v", out, err)
	}
	if len(asked) != 2 || asked[1].Line != "stop rounding" {
		t.Errorf("asked %+v", asked)
	}
	if len(acted) != 1 || acted[0].Line != "stopped rounding" || acted[0].Undo == nil {
		t.Errorf("acted %+v", acted)
	}
	if registry.AgentOf(ctx) != "assistant" {
		t.Error("the agent isn't in the call's context")
	}
}
