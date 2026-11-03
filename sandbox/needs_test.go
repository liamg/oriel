package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
)

type asks struct {
	got   agent.SandboxPlace
	needs agent.SandboxNeeds
}

func (a *asks) SandboxNeeds(_ context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	a.got = at
	return a.needs, nil
}

func (*asks) MCPServers() []agent.MCPServer { return nil }

// A provider is asked at its own directory in the sandbox, which the agent always gets with the session's pasted
// files, and sees only the host
// variables the sandbox would pass on; a blocking problem refuses the launch with its fix.
func TestNeedsAskTheProviderAtItsDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	p := &asks{needs: agent.SandboxNeeds{Mounts: []agent.Mount{{Host: "/h/x", Sandbox: "/a/x"}}}}
	o := LaunchOptions{Provider: "codex", Agent: p, StateDir: filepath.Join(root, "state"), Environ: []string{"OPENAI_API_KEY=k", "GITHUB_TOKEN=g"},
		Config: Config{Env: []string{"OPENAI_*"}}}
	n, err := Needs(context.Background(), root, root, o, agent.SandboxPlace{OS: "linux", Arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if p.got.Dir != AgentDir(root, "codex") || p.got.Root != root || p.got.StateDir != o.StateDir || p.got.OS != "linux" ||
		p.got.Cache != AgentCache("codex") || p.got.Tmp != TmpDir(root) {
		t.Errorf("asked at %+v", p.got)
	}
	if p.got.Env("OPENAI_API_KEY") != "k" || p.got.Env("GITHUB_TOKEN") != "" {
		t.Errorf("the provider sees variables the sandbox wouldn't pass")
	}
	if len(n.Mounts) != 3 || n.Mounts[0] != (agent.Mount{Host: AgentDir(root, "codex")}) {
		t.Errorf("mounts = %+v", n.Mounts)
	}
	att := AttachmentsDir(o.StateDir)
	if n.Mounts[2] != (agent.Mount{Host: att, ReadOnly: true}) {
		t.Errorf("the session's attachments aren't mounted: %+v", n.Mounts)
	}
	if fi, err := os.Stat(att); err != nil || !fi.IsDir() {
		t.Errorf("the attachments folder wasn't made before the launch: %v", err)
	}
	p.needs.Problems = []agent.SandboxProblem{{Code: "x", Message: "not signed in", Fix: "sign in", Blocking: true}}
	if _, err := Needs(context.Background(), root, root, o, agent.SandboxPlace{}); err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Errorf("a blocking problem launched: %v", err)
	}
}
