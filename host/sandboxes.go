package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/sandbox"
)

// Permissions is sandboxhost.Boxes.Permissions, with a panic recovered.
func (w *Host) Permissions(ctx context.Context, id core.SessionID) (_ Permissions, err error) {
	defer w.recover("Permissions", &err)
	return w.boxes.Permissions(ctx, id)
}

// SetToolRule is sandboxhost.Boxes.SetToolRule, with a panic recovered.
func (w *Host) SetToolRule(id core.SessionID, tool, action string) (err error) {
	defer w.recover("SetToolRule", &err)
	return w.boxes.SetToolRule(id, tool, action)
}

// SetNetRule is sandboxhost.Boxes.SetNetRule, with a panic recovered.
func (w *Host) SetNetRule(id core.SessionID, host string, port int, action string, private bool) (err error) {
	defer w.recover("SetNetRule", &err)
	return w.boxes.SetNetRule(id, host, port, action, private)
}

// Workspace is sandboxhost.Boxes.Workspace, with a panic recovered.
func (w *Host) Workspace(ctx context.Context, id core.SessionID) (_ Workspace, err error) {
	defer w.recover("Workspace", &err)
	return w.boxes.Workspace(ctx, id)
}

// RebuildSandbox is sandboxhost.Boxes.RebuildSandbox, with a panic recovered.
func (w *Host) RebuildSandbox(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("RebuildSandbox", &err)
	return w.boxes.RebuildSandbox(ctx, id)
}

// RestartSandbox is sandboxhost.Boxes.RestartSandbox, with a panic recovered.
func (w *Host) RestartSandbox(id core.SessionID) (err error) {
	defer w.recover("RestartSandbox", &err)
	return w.boxes.RestartSandbox(id)
}

// SandboxShell is sandboxhost.Boxes.SandboxShell, with a panic recovered.
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
