package sessions_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// The app mirrors the host's sessions, starts one through a command, and the window follows it.
func TestTheWindowFollowsTheSessionInFront(t *testing.T) {
	lw := rig.NewHost(t)
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err := w.Do(func() error {
		return w.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}})
	})
	if err != nil {
		t.Fatal(err)
	}
	st := w.UntilState("the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	se, _ := st.Session("rounding")
	p, _ := se.Snapshot().Primary()
	if !strings.HasSuffix(p.Dir, filepath.Join("workspaces", "rounding", "repos", "pricing")) {
		t.Fatalf("project = %+v", p)
	}
	w.TypeKeys("<Space><Space>")
	w.WaitFrame("the worktree's files in the picker", func(s string) bool {
		return !strings.Contains(s, "Open Directory") && !strings.Contains(s, "Opening rounding")
	})
	w.WaitProject(p.Dir)
}

// With a session host, Open Directory is a direct session with no agent, the same one each
// time, so committing and raising a PR work there as in any session.
func TestOpenDirectoryIsADirectSession(t *testing.T) {
	lw := rig.NewHost(t)
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	open := func() core.SessionState {
		if err := w.Do(func() error {
			return w.Host.Run(context.Background(), "directory.open", registry.Args{"path": lw.Repo})
		}); err != nil {
			t.Fatal(err)
		}
		st := w.UntilState("a direct session in front", func(st core.State) bool {
			se, ok := st.Session(st.Focus)
			return ok && se.Snapshot().Kind == core.KindDirect
		})
		se, _ := st.Session(st.Focus)
		return se.Snapshot()
	}
	first := open()
	if first.Dir != lw.Repo || first.Agent.Provider != "" || len(first.Projects) != 1 || first.Projects[0].Branch != "main" {
		t.Fatalf("direct session = %+v, want the repo itself on main, with no agent", first)
	}
	w.WaitProject(lw.Repo)
	if again := open(); again.ID != first.ID {
		t.Errorf("opening it again started %s, want %s again", again.ID, first.ID)
	}
	if n := len(w.Store.State().Sessions); n != 1 {
		t.Errorf("%d sessions, want the one", n)
	}
}

// oriel <dir> opens on the directory's direct session too.
func TestLaunchOnADirectoryIsADirectSession(t *testing.T) {
	lw := rig.NewHost(t)
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: lw.Repo, Config: apptest.Connect(lw.W)})
	st := w.UntilState("a direct session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Kind == core.KindDirect && se.Snapshot().Dir == lw.Repo
	})
	if len(st.Sessions) != 1 {
		t.Errorf("sessions = %+v, want the direct one", st.Sessions)
	}
}

// A Permission hook answers before the user is asked.
func TestPermissionHooksAnswerForTheUser(t *testing.T) {
	lw := rig.NewHost(t)
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W), Setup: func(h plugin.Host) {
		h.Hook(registry.Hook{Event: "Permission", Kind: registry.Decide, Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			p := ev.(registry.PermissionEvent)
			if p.Tool == "Bash" && strings.HasPrefix(p.Input, "go test ") {
				return registry.Verdict{Decided: true, Value: "allow"}, nil
			}
			return registry.Verdict{}, nil
		}})
	}})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	w.Do(func() error {
		return w.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo})
	})
	a := lw.Fake.WaitStarted(t)
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	if d := apptest.Recv(t, "the hook's answer", a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: in})); !d.Allow {
		t.Errorf("the hook's allow came back as %+v", d)
	}
	rm, _ := json.Marshal(map[string]string{"command": "rm -rf build"})
	a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: rm})
	w.UntilState("a card for what the hook left", func(st core.State) bool {
		se, _ := st.Session("w")
		return se != nil && len(se.Snapshot().Asks) == 1
	})
}

// A plugin's agent tool is offered to the agent, and runs in the app for the session that asked.
func TestAgentToolsRunInTheApp(t *testing.T) {
	lw := rig.NewHost(t)
	var session string
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W), Setup: func(h plugin.Host) {
		h.Contribute(registry.AgentTool{Name: "staging_logs", Desc: "Tail the staging logs", Params: map[string]string{"service": "string", "lines": "number?"},
			ReadOnly: true, Run: func(ctx registry.Ctx, p map[string]any) (any, error) {
				session = registry.SessionOf(ctx)
				return "logs of " + p["service"].(string), nil
			}})
	}})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	w.Do(func() error {
		return w.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo})
	})
	a := lw.Fake.WaitStarted(t)
	var tool agent.Tool
	for _, x := range a.Spec.Tools {
		if x.Name == "staging_logs" {
			tool = x
		}
	}
	if !tool.ReadOnly || !strings.Contains(string(tool.Schema), `"required":["service"]`) {
		t.Fatalf("tool = %+v %s", tool, tool.Schema)
	}
	if got := a.Call(t, "staging_logs", map[string]any{"service": "api"}); got != "logs of api" || session != "w" {
		t.Errorf("got %q for session %q", got, session)
	}
}

// An agent's turns fire the AgentTurnEnd hook, with the session, from Lua or Go.
func TestAgentTurnEndFires(t *testing.T) {
	lw := rig.NewHost(t)
	ended := make(chan string, 4)
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W), Setup: func(h plugin.Host) {
		h.Hook(registry.Hook{Event: "AgentTurnEnd", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			ended <- ev.(kit.AgentTurnEnd).Session
			return registry.Verdict{}, nil
		}})
	}})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	w.Do(func() error {
		return w.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo})
	})
	a := lw.Fake.WaitStarted(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	if got := apptest.Recv(t, "AgentTurnEnd", ended); got != "w" {
		t.Errorf("AgentTurnEnd for %q", got)
	}
}
