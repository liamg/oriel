package claudebox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
)

func fakeClaude(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 2.1.300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	return bin
}

// In the OS's sandbox Claude runs the host's claude, with its config in the sandbox's directory, the user's own
// Claude files read-only under it, its transcripts written back to the host's, Oriel's mod read-only, and its
// endpoints and credentials' variables passed on.
func TestNeedsOnTheHostsLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := fakeClaude(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "ws")
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	at := agent.SandboxPlace{Root: root, Cwd: root, Dir: Dir(root), Shared: true, Env: func(string) string { return "" }}
	if err := os.MkdirAll(at.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCreds(t, filepath.Join(home, ".claude", credentialsFile), "refresh")
	var box Box
	n, err := box.Needs(context.Background(), at, "/state/claude-mod", "")
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(bin); n.Bins["claude"] != real {
		t.Errorf("bins = %v, want the host's %s", n.Bins, real)
	}
	if n.Env["CLAUDE_CONFIG_DIR"] != at.Dir {
		t.Errorf("env = %v", n.Env)
	}
	transcripts := filepath.Join(home, ".claude", "projects", sandbox.ProjectDirName(root))
	for _, want := range []agent.Mount{
		{Host: filepath.Join(home, ".claude", "skills"), Sandbox: filepath.Join(at.Dir, "skills"), ReadOnly: true},
		{Host: transcripts, Sandbox: filepath.Join(at.Dir, "projects", sandbox.ProjectDirName(root))},
		{Host: "/state/claude-mod", ReadOnly: true},
	} {
		if !slices.Contains(n.Mounts, want) {
			t.Errorf("no mount %+v in %+v", want, n.Mounts)
		}
	}
	if !slices.Contains(n.Hosts, "api.anthropic.com") || !slices.Contains(n.PassEnv, "ANTHROPIC_*") {
		t.Errorf("hosts %v, env %v", n.Hosts, n.PassEnv)
	}
	if _, err := os.Stat(filepath.Join(at.Dir, credentialsFile)); err != nil {
		t.Errorf("the host's sign-in wasn't handed over: %v", err)
	}
	if _, err := os.Stat(filepath.Join(at.Dir, "settings.json")); err != nil {
		t.Errorf("no settings: %v", err)
	}
}

// With no claude and no way to sign in, a sandboxed Claude is refused before it starts, saying what fixes it.
func TestNeedsRefuseWithNoClaudeOrLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	var box Box
	at := agent.SandboxPlace{Shared: true, Env: func(string) string { return "" }}
	n, err := box.Needs(context.Background(), at, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, p := range n.Problems {
		if p.Blocking {
			codes = append(codes, p.Code)
		}
	}
	if !slices.Equal(codes, []string{"claude-missing", "claude-token-missing"}) || len(n.Bins) != 0 {
		t.Errorf("blocking %v, bins %v", codes, n.Bins)
	}
}
