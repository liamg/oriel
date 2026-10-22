package sandboxhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// Workspace is where a session's agent works: its directories, and the sandbox it runs in.
type Workspace struct {
	Root      string
	Sandboxed bool
	// Running says the agent's sandbox is up now.
	Running bool
	Backend string
	Paths   []WorkspacePath
	// Rebuild says the backend can build the session's image again.
	Rebuild bool
	sandbox.Description
}

// WorkspacePath is a directory a session uses: on the host, and where its sandboxed agent finds it.
type WorkspacePath struct {
	Name  string
	Host  string
	Agent string // "" where the agent sees it at the same path
	Note  string
}

// Workspace reads session id's workspace. A session that isn't sandboxed has its directories only.
func (x *Boxes) Workspace(ctx context.Context, id core.SessionID) (Workspace, error) {
	se, err := x.deps.State.Session(id)
	if err != nil {
		return Workspace{}, err
	}
	root := hostcore.AgentDir(se)
	ws := Workspace{Root: root}
	for _, p := range se.Projects {
		note := p.Branch
		if p.Repo != "" && p.Repo != p.Dir {
			note += " · worktree of " + p.Repo
		}
		ws.Paths = append(ws.Paths, WorkspacePath{Name: p.Name, Host: p.Dir, Note: note})
	}
	if root == "" {
		return ws, nil
	}
	rec, ok := sandbox.Load(root)
	ws.Sandboxed = ok && rec.Sandboxed()
	if !ws.Sandboxed {
		return ws, nil
	}
	cfg := x.Config()
	b := x.of(id)
	if b != nil {
		cfg = b.cfg
	} else if se.Agent.Backend != "" {
		cfg.Backend = se.Agent.Backend
	}
	ws.Running = b != nil
	backend, err := x.backendFor(b, cfg)
	if err == nil {
		ws.Backend = backend.Name()
		if d, ok := backend.(sandbox.Describer); ok {
			ws.Description = d.Describe(ctx, root, cfg)
		}
		_, ws.Rebuild = backend.(sandbox.Rebuilder)
	}
	if state, ok := sandbox.StateDir(root); ok {
		ws.Paths = append(ws.Paths, WorkspacePath{Name: "State", Host: state, Note: "the session's comments, drafts and attachments"})
	}
	transcripts := filepath.Join(sandbox.HostClaudeDir(), "projects", sandbox.ProjectDirName(root))
	for _, p := range []WorkspacePath{
		{Name: "Scratchpad", Host: sandbox.TmpDir(root), Note: "Claude Code's temp dir"},
		{Name: "Sandbox", Host: sandbox.Dir(root), Note: "the sandbox's record, sockets and grants"},
		{Name: "Home", Host: sandbox.HomeDir(root), Note: "the agent's $HOME"},
		{Name: "Claude config", Host: sandbox.ClaudeDir(root)},
		{Name: "Transcripts", Host: transcripts},
		{Name: "Go cache", Host: sandbox.GoCache(), Note: "shared by every sandbox"},
	} {
		p.Agent = ws.Places[p.Host]
		ws.Paths = append(ws.Paths, p)
	}
	return ws, nil
}

// backendFor is the running sandbox's backend, else the one cfg names.
func (x *Boxes) backendFor(b *Box, cfg sandbox.Config) (sandbox.Backend, error) {
	if b != nil {
		return b.backend, nil
	}
	f, ok := x.deps.Factory().(interface {
		BackendFor(sandbox.Config) (sandbox.Backend, error)
	})
	if !ok {
		return nil, errors.New("no sandbox backends")
	}
	return f.BackendFor(cfg)
}

// RebuildSandbox builds session id's image again, skipping the cache, then restarts its agent on it once the turn
// ends, resumed.
func (x *Boxes) RebuildSandbox(ctx context.Context, id core.SessionID) error {
	se, err := x.deps.State.Session(id)
	if err != nil {
		return err
	}
	b := x.of(id)
	cfg := x.Config()
	if b != nil {
		cfg = b.cfg
	}
	backend, err := x.backendFor(b, cfg)
	if err != nil {
		return err
	}
	r, ok := backend.(sandbox.Rebuilder)
	if !ok {
		return fmt.Errorf("the %s sandbox has no image to build", backend.Name())
	}
	if err := r.Rebuild(ctx, hostcore.AgentDir(se), cfg); err != nil {
		return err
	}
	if b != nil {
		b.RestartWhenIdle("restarted on its rebuilt image")
	}
	return nil
}

// RestartSandbox restarts session id's agent in a new sandbox once its turn ends, resumed.
func (x *Boxes) RestartSandbox(id core.SessionID) error {
	b := x.of(id)
	if b == nil {
		return fmt.Errorf("session %s's agent isn't running in a sandbox", id)
	}
	b.RestartWhenIdle("restarted from its workspace")
	return nil
}

// SandboxShell is the command that runs a shell in session id's running sandbox, in cwd (the workspace when cwd is
// outside it): what a terminal runs for a sandboxed session.
func (x *Boxes) SandboxShell(ctx context.Context, id core.SessionID, cwd string) (sandbox.Command, error) {
	b := x.of(id)
	if b == nil {
		return sandbox.Command{}, fmt.Errorf("session %s's agent isn't running in a sandbox", id)
	}
	sh, ok := b.backend.(sandbox.Sheller)
	if !ok {
		return sandbox.Command{}, fmt.Errorf("the %s sandbox can't run a terminal", b.backend.Name())
	}
	if rel, err := filepath.Rel(b.root, cwd); cwd == "" || err != nil || strings.HasPrefix(rel, "..") {
		cwd = b.root
	}
	return sh.Shell(ctx, b.root, cwd, sandbox.LaunchOptions{Environ: os.Environ(), Config: b.cfg})
}
