package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/worker"
)

const oneOff = "This happens once: later sessions reuse the image and start in seconds."

// holdExec starts the agent's process only once hold closes, having reported a slow step, as a sandbox
// building its image does.
type holdExec struct {
	agent.Exec
	hold chan struct{}
}

func (e holdExec) Start(ctx context.Context, _ *exec.Cmd) (agent.Process, error) {
	sandbox.Report(ctx, sandbox.Progress{Step: "Building the sandbox image", Note: oneOff})
	select {
	case <-e.hold:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return hostexec.Exec{}.Start(ctx, exec.Command("true"))
}

// ui.md 04: Start runs session.start and the launcher closes with the session in front, though its agent is still
// starting. Meanwhile the request says it's queued for an agent that's starting, a card with a spinner says what
// the start is doing, for how long, and that it happens once, mission control's card says STARTING, and the raw
// terminal says there's none yet. Once the agent is up it gets the request and the card goes.
func TestLauncherClosesWhileTheAgentStarts(t *testing.T) {
	hold := make(chan struct{})
	fake := agenttest.NewFake()
	fake.Launch = true
	lw := newWorkerWith(t, worker.Options{Exec: holdExec{hold: hold}}, fake)
	lw.fake = fake
	if _, err := lw.client(t).AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	defer func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	}()
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.do(func() error {
		return r.host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})

	r.typeKeys("<D-n>")
	r.waitText("NEW SESSION")
	r.clickTop("Work")
	r.latest("Work picked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
	r.clickTop(promptPlaceholder)
	r.typeText("Fix stale search results")
	r.clickTop("+ add ▾")
	r.clickTop("pricing")
	r.latest("the repo's chip", func(f headless.Frame) bool { return strings.Contains(f.Text(), "pricing\n✕") })
	r.clickTop("Start Work")

	r.waitGone("NEW SESSION")
	st := waitState(t, r.store, "the session in front, starting", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Agent.Starting != nil
	})
	id := st.Focus
	r.waitText("Building the sandbox image…")
	r.waitText(oneOff)
	r.waitText("queued · agent starting")

	err := r.do(func() error {
		return r.host.Run(context.Background(), "agent.terminal", registry.Args{"id": string(id)})
	})
	if err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Errorf("the raw terminal of a starting agent: %v", err)
	}

	r.typeKeys("<D-K>")
	r.waitText("STARTING · ")

	close(hold)
	a := fake.WaitStarted(t)
	if in := a.WaitSent(t); !strings.Contains(in.Text, "Fix stale search results") {
		t.Errorf("the agent's first input: %q", in.Text)
	}
	r.waitGone("Building the sandbox image…")
}
