package acp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/agent"
)

// In a sandbox an ACP agent gets what its config says: the host's program under the OS's sandbox (the image's in a
// container), the home entries it keeps its sign-in in at the sandbox's home, its hosts and variables.
func TestSandboxNeedsAreTheConfigs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(t.TempDir(), "gemini")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	p := &Provider{Name: "gemini", Config: Config{Command: []string{"gemini", "--experimental-acp"}, Hosts: []string{"g.example"},
		PassEnv: []string{"GEMINI_*"}, Tools: []string{"npm:@google/gemini-cli@0.9.0"}, Home: []string{".gemini", "../escape", "/abs"}}}
	n, err := p.SandboxNeeds(context.Background(), agent.SandboxPlace{Shared: true, Home: "/sbx/home"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Bins["gemini"] != bin || len(n.Hosts) != 1 || len(n.PassEnv) != 1 || len(n.Tools) != 1 {
		t.Errorf("needs = %+v", n)
	}
	if len(n.Mounts) != 1 || n.Mounts[0] != (agent.Mount{Host: filepath.Join(home, ".gemini"), Sandbox: "/sbx/home/.gemini"}) {
		t.Errorf("mounts = %+v", n.Mounts)
	}
	if n, _ := p.SandboxNeeds(context.Background(), agent.SandboxPlace{OS: "linux", Arch: "amd64", Home: "/home/u"}); len(n.Bins) != 0 {
		t.Errorf("a container got the host's program: %v", n.Bins)
	}
	t.Setenv("PATH", t.TempDir())
	if n, _ := p.SandboxNeeds(context.Background(), agent.SandboxPlace{Shared: true}); len(n.Problems) != 1 || !n.Problems[0].Blocking {
		t.Errorf("problems with no program = %+v", n.Problems)
	}
}
