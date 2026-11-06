package claude

import (
	"context"
	"sync"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/eventq"
)

// A tool offered after the agent started (a plugin that loaded later) reaches it: its next wait asks the mod to say
// hello again, which hands it over, and a call to it runs.
func TestToolsAddedAfterTheStartReachTheAgent(t *testing.T) {
	var mu sync.Mutex
	tools := []agent.Tool{agenttest.Echo}
	now := func() []agent.Tool {
		mu.Lock()
		defer mu.Unlock()
		return tools
	}
	s := &session{spec: agent.StartSpec{ToolsNow: now}, events: eventq.New(), wake: make(chan struct{}, 1)}
	if got := len(s.hello(helloIn{}).Tools); got != 1 {
		t.Fatalf("hello's tools = %d, want 1", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.wait(ctx).Kind; got != "timeout" {
		t.Errorf("with the tools unchanged, wait = %q", got)
	}

	late := agenttest.Echo
	late.Name = "linear_issue"
	mu.Lock()
	tools = append(tools, late)
	mu.Unlock()
	if got := s.wait(context.Background()).Kind; got != "tools" {
		t.Fatalf("with a tool added, wait = %q, want tools", got)
	}
	out := s.hello(helloIn{})
	if len(out.Tools) != 2 || out.Tools[1].Name != "linear_issue" {
		t.Errorf("hello again = %+v", out.Tools)
	}
	if got := s.wait(ctx).Kind; got != "timeout" {
		t.Errorf("once handed over, wait = %q", got)
	}
	if r := s.callTool(context.Background(), toolIn{Name: "linear_issue", Input: []byte(`{"text":"hi"}`)}); r.IsError {
		t.Errorf("calling the late tool: %+v", r)
	}
}
