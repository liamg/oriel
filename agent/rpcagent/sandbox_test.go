package rpcagent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// boxed is a provider that says what its agent needs in a sandbox, asking its place's callbacks as it does.
type boxed struct {
	*agenttest.Fake
	needs agent.SandboxNeeds
	got   agent.SandboxPlace
	env   string
}

func (b *boxed) SandboxNeeds(_ context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	b.got = at
	b.env = at.Env("OPENAI_API_KEY")
	at.Report("Downloading", "once")
	return b.needs, nil
}

func (b *boxed) MCPServers() []agent.MCPServer {
	return []agent.MCPServer{{Name: "docs", Source: "user", Config: json.RawMessage(`{"command":"docs"}`), Vars: map[string]string{"R": "/r"}}}
}

// What a provider needs in a sandbox crosses whole, every field: the place it's asked at, the host variables it
// asks for and the progress it reports going back over the protocol as it asks.
func TestSandboxNeedsCrossWhole(t *testing.T) {
	want := agent.SandboxNeeds{Bins: map[string]string{"agent": "/b/agent"}, Tools: []string{"node@22"},
		Mounts: []agent.Mount{{Host: "/h", Sandbox: "/s", ReadOnly: true}}, Env: map[string]string{"A": "1"},
		PassEnv: []string{"OPENAI_*"}, SkipEnv: []string{"OPENAI_ORG"}, URLEnv: []string{"OPENAI_BASE_URL"}, URLs: []string{"http://x"},
		Hosts: []string{"api.example"}, Problems: []agent.SandboxProblem{{Code: "c", Message: "m", Fix: "f", Blocking: true}}}
	p := &boxed{Fake: agenttest.NewFake(), needs: want}
	c := behind(t, "fake", p).(agent.Sandboxed)
	var reported []string
	at := agent.SandboxPlace{Root: "/w", Cwd: "/w/p", Dir: "/d", Home: "/home", Tmp: "/t", OS: "linux", Arch: "arm64", UID: 501,
		StateDir: "/st", Cache: "/c", Report: func(step, note string) { reported = append(reported, step+"/"+note) },
		Env: func(n string) string { return map[string]string{"OPENAI_API_KEY": "k"}[n] }}
	got, err := c.SandboxNeeds(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("needs = %+v\nwant %+v", got, want)
	}
	if p.got.Root != "/w" || p.got.Dir != "/d" || p.got.Tmp != "/t" || p.got.Arch != "arm64" || p.got.UID != 501 || p.got.StateDir != "/st" ||
		p.got.Cache != "/c" || p.env != "k" {
		t.Errorf("asked at %+v, env %q", p.got, p.env)
	}
	if len(reported) != 1 || reported[0] != "Downloading/once" {
		t.Errorf("reported %v", reported)
	}
	if ms := c.MCPServers(); len(ms) != 1 || ms[0].Name != "docs" || ms[0].Vars["R"] != "/r" || string(ms[0].Config) != `{"command":"docs"}` {
		t.Errorf("servers = %+v", ms)
	}
}
