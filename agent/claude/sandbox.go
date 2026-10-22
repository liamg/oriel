package claude

import (
	"context"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/claudebox"
)

var (
	_ agent.Featured  = (*Provider)(nil)
	_ agent.Sandboxed = (*Provider)(nil)
)

// Features: Claude's terminal is the user's to watch and type in, it forks with no tools ($.model.fork), and runs
// in a sandbox. Pasted images reach it as their paths; it signs in at its terminal (/login).
func (*Provider) Features() agent.Features {
	return agent.Features{Terminal: true, Fork: true, Sandbox: true, Notes: "CLAUDE.md"}
}

func (p *Provider) pinned() string {
	if p.SandboxVersion == nil {
		return ""
	}
	return p.SandboxVersion()
}

// SandboxNeeds are claude in the sandbox with its config, sign-in and Oriel's mod (StateDir's claude-mod, where
// start writes it).
func (p *Provider) SandboxNeeds(ctx context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	mod := ""
	if at.StateDir != "" {
		mod = filepath.Join(at.StateDir, "claude-mod")
	}
	return p.box.Needs(ctx, at, mod, p.pinned())
}

func (*Provider) MCPServers() []agent.MCPServer {
	home, _ := os.UserHomeDir()
	return claudebox.MCPServers(home)
}
