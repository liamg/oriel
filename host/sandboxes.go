package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/sandbox"
)

// Permissions reads session id's sandbox permissions. A session whose agent isn't running in a sandbox has none.
func (w *Host) Permissions(ctx context.Context, id core.SessionID) (_ Permissions, err error) {
	defer w.recover("Permissions", &err)
	return w.boxes.Permissions(ctx, id)
}

// SetToolRule sets session id's own rule for a gateway tool: allow, ask or deny, or "" to drop it.
func (w *Host) SetToolRule(id core.SessionID, tool, action string) (err error) {
	defer w.recover("SetToolRule", &err)
	return w.boxes.SetToolRule(id, tool, action)
}

// SetNetRule allows or blocks host[:port] for the rest of session id ("allow", "block"), or drops the session's
// rule for it ("").
func (w *Host) SetNetRule(id core.SessionID, host string, port int, action string, private bool) (err error) {
	defer w.recover("SetNetRule", &err)
	return w.boxes.SetNetRule(id, host, port, action, private)
}

// Workspace reads session id's workspace. A session that isn't sandboxed has its directories only.
func (w *Host) Workspace(ctx context.Context, id core.SessionID) (_ Workspace, err error) {
	defer w.recover("Workspace", &err)
	return w.boxes.Workspace(ctx, id)
}

// RebuildSandbox builds session id's image again, skipping the cache, then restarts its agent on it once the turn
// ends, resumed.
func (w *Host) RebuildSandbox(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("RebuildSandbox", &err)
	return w.boxes.RebuildSandbox(ctx, id)
}

// RestartSandbox restarts session id's agent in a new sandbox once its turn ends, resumed.
func (w *Host) RestartSandbox(id core.SessionID) (err error) {
	defer w.recover("RestartSandbox", &err)
	return w.boxes.RestartSandbox(id)
}

// SandboxShell is the command that runs a shell in session id's running sandbox, in cwd (the workspace when cwd is
// outside it): what a terminal runs for a sandboxed session.
func (w *Host) SandboxShell(ctx context.Context, id core.SessionID, cwd string) (_ sandbox.Command, err error) {
	defer w.recover("SandboxShell", &err)
	return w.boxes.SandboxShell(ctx, id, cwd)
}

// SetSandboxConfig is the sandbox's settings, as the app resolved them from its sandbox.* options. Sandboxes
// opened after it use them.
func (w *Host) SetSandboxConfig(cfg sandbox.Config) (err error) {
	defer w.recover("SetSandboxConfig", &err)
	return w.boxes.SetConfig(cfg)
}

// SandboxConfig is the sandbox settings the app last sent; the defaults if it never has.
func (w *Host) SandboxConfig() sandbox.Config {
	defer w.recover("SandboxConfig", nil)
	return w.boxes.Config()
}

// SweepSandboxes removes what sandboxes no session uses left behind, now and then every interval, until ctx ends.
func (w *Host) SweepSandboxes(ctx context.Context, every time.Duration) {
	defer w.recover("SweepSandboxes", nil)
	w.boxes.Sweep(ctx, every)
}

// Sandboxes is the SandboxFactory backed by the real sandbox: a sandbox.Backend that confines each agent run, the
// egress proxy's host side, the gateway and the session socket. Attach the host before a session opens one.
type Sandboxes struct{ *sandboxhost.Factory }

// NewSandboxFactory makes the real sandbox factory.
func NewSandboxFactory(config func() sandbox.Config) *Sandboxes {
	return &Sandboxes{sandboxhost.NewFactory(config)}
}

// Attach gives the factory the host whose sessions it serves.
func (s *Sandboxes) Attach(w *Host) { s.Factory.Attach(w.sandboxDeps()) }
