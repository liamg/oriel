package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/sandbox"
)

// SetSandboxConfig is the sandbox's settings, as the app resolved them from its sandbox.* options. Sandboxes
// opened after it use them.
func (w *Host) SetSandboxConfig(cfg sandbox.Config) (err error) {
	defer w.env.Recover("SetSandboxConfig", &err)
	return w.boxesPart.SetConfig(cfg)
}

// SandboxConfig is the sandbox settings the app last sent; the defaults if it never has.
func (w *Host) SandboxConfig() sandbox.Config {
	defer w.env.Recover("SandboxConfig", nil)
	return w.boxesPart.Config()
}

// SweepSandboxes removes what sandboxes no session uses left behind, now and then every interval, until ctx ends.
func (w *Host) SweepSandboxes(ctx context.Context, every time.Duration) {
	defer w.env.Recover("SweepSandboxes", nil)
	w.boxesPart.Sweep(ctx, every)
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
