// Package sandboxhost is the session host's side of sandboxes: the factory that launches a sandboxed agent
// with its egress proxy, gateway and session socket, the grants and rules a session's sandbox keeps, and the sweep of
// what sandboxes no session uses left behind.
package sandboxhost

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// Deps are what the sandboxes need of the session host they serve.
type Deps struct {
	Env   hostcore.Env
	State hostcore.State
	Keyed hostcore.Keyed
	// Config is the sandbox settings the app last sent.
	Config func() sandbox.Config
	// Factory is the host's sandbox factory; nil when this machine can't run one.
	Factory func() hostcore.SandboxFactory
	// Live is session id's sandbox while its agent runs in one, else nil.
	Live func(id core.SessionID) hostcore.Sandbox
	// Running are the sandboxes agents run in now.
	Running func() []hostcore.Sandbox
	// Repos are the registered checkouts.
	Repos func() []core.Repo
	// AddRepo brings a repo into a session.
	AddRepo func(ctx context.Context, id core.SessionID, ref core.RepoID) error
	// Note puts an item in a running session's transcript.
	Note func(id core.SessionID, it core.Item)
	// SetAgent changes a running session's agent state.
	SetAgent func(id core.SessionID, fn func(a *core.AgentState))
	// RestartWhenIdle restarts session id's agent, resumed, once its turn ends, if it still runs in sb.
	RestartWhenIdle func(id core.SessionID, sb hostcore.Sandbox, why string)
}

// Boxes are the host's sessions' sandboxes, and the sandbox settings the app last sent.
type Boxes struct {
	deps  Deps
	cfgMu sync.Mutex
	cfg   sandbox.Config
}

// New reads the sandbox settings the app last sent.
func New(deps Deps) (*Boxes, error) {
	x := &Boxes{deps: deps}
	return x, x.load()
}

// Deps are the deps the boxes were made with, for a factory to attach to.
func (x *Boxes) Deps() Deps { return x.deps }

// of is session id's sandbox while its agent runs in one.
func (x *Boxes) of(id core.SessionID) *Box {
	b, _ := x.deps.Live(id).(*Box)
	return b
}

// Of is session id's sandbox while its agent runs in one of this package's, else nil.
func Of(sb hostcore.Sandbox) *Box {
	b, _ := sb.(*Box)
	return b
}

// Sweep removes what sandboxes no session uses left behind, now and then every interval, until ctx ends.
func (x *Boxes) Sweep(ctx context.Context, every time.Duration) {
	f := x.deps.Factory()
	if f == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		func() {
			defer x.deps.Env.Recover("SweepSandboxes", nil)
			for _, gone := range f.Sweep(ctx, x.attached, x.known) {
				x.deps.Env.Log.Info("sandbox sweep removed", "what", gone)
			}
		}()
		select {
		case <-ctx.Done():
			return
		case <-x.deps.Env.Closed:
			return
		case <-t.C:
		}
	}
}

// attached says whether a running agent's sandbox is root's.
func (x *Boxes) attached(root string) bool {
	root = filepath.Clean(root)
	return slices.ContainsFunc(x.deps.Running(), func(sb hostcore.Sandbox) bool {
		b, ok := sb.(*Box)
		return ok && filepath.Clean(b.root) == root
	})
}

// known says whether a session, archived or not, runs its agent in root.
func (x *Boxes) known(root string) bool {
	root = filepath.Clean(root)
	return slices.ContainsFunc(x.deps.State.Snapshot().Sessions, func(se core.SessionState) bool {
		d := hostcore.AgentDir(se)
		return d != "" && filepath.Clean(d) == root
	})
}
