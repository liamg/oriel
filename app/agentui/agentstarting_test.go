package agentui_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/sandbox"
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

// Start runs session.start and the launcher closes with the session in front, though its agent is still
// starting. Meanwhile the request says it's queued for an agent that's starting, a card with a spinner says what
// the start is doing, for how long, and that it happens once, mission control's card says STARTING, and the raw
// terminal says there's none yet. Once the agent is up it gets the request and the card goes.
func TestLauncherClosesWhileTheAgentStarts(t *testing.T) {
	hold := make(chan struct{})
	fake := agenttest.NewFake()
	fake.Launch = true
	lw := rig.NewHostWith(t, host.Options{Exec: holdExec{hold: hold}}, fake)
	lw.Fake = fake
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	defer func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	}()
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.Do(func() error {
		return r.Host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})

	r.TypeKeys("<D-n>")
	r.WaitText("NEW SESSION")
	r.ClickTop("Work")
	r.Latest("Work picked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
	r.ClickTop("What should the agent do? A ticket ID goes in the ticket field.") // the launcher's prompt
	r.TypeText("Fix stale search results")
	r.ClickTop("pricing")
	r.Latest("the repo ticked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "1 selected") })
	r.ClickTop("Start Work")

	r.WaitGone("NEW SESSION")
	st := r.UntilState("the session in front, starting", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Agent.Starting != nil
	})
	id := st.Focus
	r.WaitText("Building the sandbox image…")
	r.WaitText(oneOff)
	r.WaitText("queued · agent starting")

	err := r.Do(func() error {
		return r.Host.Run(context.Background(), "agent.terminal", registry.Args{"id": string(id)})
	})
	if err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Errorf("the raw terminal of a starting agent: %v", err)
	}

	r.TypeKeys("<D-K>")
	r.WaitText("STARTING · ")

	close(hold)
	a := fake.WaitStarted(t)
	if in := a.WaitSent(t); !strings.Contains(in.Text, "Fix stale search results") {
		t.Errorf("the agent's first input: %q", in.Text)
	}
	r.WaitGone("Building the sandbox image…")
}
