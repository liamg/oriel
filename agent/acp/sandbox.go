package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/agent"
)

var _ agent.Sandboxed = (*Provider)(nil)

// SandboxNeeds are what the config says: under the OS's sandbox the host's program, in a container the tools it
// installs (else the image's program);
// the user's home entries it names at the sandbox's home, read-write, as its own config and sign-in; its hosts and
// the variables it inherits.
func (p *Provider) SandboxNeeds(_ context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	n := agent.SandboxNeeds{Hosts: p.Config.Hosts, PassEnv: p.Config.PassEnv, Tools: p.Config.Tools}
	name := p.Config.Command[0]
	if at.Shared {
		path, err := exec.LookPath(name)
		if err != nil {
			n.Problems = append(n.Problems, agent.SandboxProblem{Code: "agent-missing", Blocking: true,
				Message: name + " is not installed on this machine", Fix: "Install it; this sandbox runs the host's."})
			return n, nil
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		n.Bins = map[string]string{filepath.Base(name): path}
	}
	home, _ := os.UserHomeDir()
	for _, e := range p.Config.Home {
		e = filepath.Clean(e)
		if filepath.IsAbs(e) || e == "." || strings.HasPrefix(e, "..") {
			continue
		}
		host := filepath.Join(home, e)
		if err := os.MkdirAll(host, 0o700); err != nil {
			return n, err
		}
		n.Mounts = append(n.Mounts, agent.Mount{Host: host, Sandbox: filepath.Join(at.Home, e)})
	}
	return n, nil
}

// MCPServers: an ACP agent's own servers are its to start, in the sandbox; none come through the gateway.
func (*Provider) MCPServers() []agent.MCPServer { return nil }
