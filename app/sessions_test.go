package app

import (
	"context"
	"encoding/json"
	"github.com/liamg/oriel/host/sysload"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// localHost is a session host with the fake provider and a fixture repo, given to the app as Oriel gives it.
type localHost struct {
	w    *host.Host
	fake *agenttest.Fake
	repo string
}

// newLocalHost is a session host with the fake provider, or the fakes given (named as the providers they stand for).
func newLocalHost(t *testing.T, fakes ...*agenttest.Fake) *localHost {
	t.Helper()
	return newHostWith(t, host.Options{}, fakes...)
}

// newHostWith is newLocalHost with options of its own: its repos directory, its clock.
func newHostWith(t *testing.T, o host.Options, fakes ...*agenttest.Fake) *localHost {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	repo := filepath.Join(t.TempDir(), "pricing")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	lw := &localHost{fake: agenttest.NewFake(), repo: repo}
	providers := &agent.Providers{}
	if len(fakes) == 0 {
		fakes = []*agenttest.Fake{lw.fake}
	}
	for _, f := range fakes {
		providers.Register(f)
	}
	o.Home, o.Providers = t.TempDir(), providers
	if o.Exec == nil {
		o.Exec = nopExec{}
	}
	o.Packs = func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }
	if o.Load == nil {
		o.Load = steadyLoad
	}
	if o.GitHub == nil {
		hub := ghtest.New(t) // never the user's own gh
		o.GitHub = func(string, []string) gh.Client { return hub.Client() }
	}
	w, err := host.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	lw.w = w
	return lw
}

type nopExec struct{ agent.Exec }

// steadyLoad is a machine's load that doesn't change, so a scene showing it draws the same each run.
func steadyLoad() sysload.Reading {
	return sysload.Reading{CPU: 34, Cores: 12, MemUsed: 11 << 30, MemTotal: 32 << 30}
}

// connect is a Config change that gives the app the session host, and its store.
func (lw *localHost) connect(*testing.T) func(*Config) {
	return func(cfg *Config) {
		cfg.Host, cfg.Store = lw.w, lw.w.Store()
	}
}

func (s *errScene) machine() *machinePlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*machinePlugin); ok {
			return x
		}
	}
	return nil
}

// hostScene is the app with a session host, the scene's state in a temporary directory.
func hostScene(t *testing.T, change ...func(*Config)) (*errScene, *localHost) {
	t.Helper()
	lw := newLocalHost(t)
	state := t.TempDir()
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		lw.connect(t)(cfg)
		cfg.StateDir = state
		for _, f := range change {
			f(cfg)
		}
	})
	return s, lw
}

func waitState(t *testing.T, s core.Store, what string, ok func(core.State) bool) core.State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := s.State()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AI-1: the app mirrors the host's sessions, starts one through a command, and the window follows it.
func TestTheWindowFollowsTheSessionInFront(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.repo}})
	})
	if err != nil {
		t.Fatal(err)
	}
	st := waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	se, _ := st.Session("rounding")
	p, _ := se.Snapshot().Primary()
	if !strings.HasSuffix(p.Dir, filepath.Join("workspaces", "rounding", "repos", "pricing")) {
		t.Fatalf("project = %+v", p)
	}
	r.typeKeys("<Space><Space>")
	r.waitFrame("the worktree's files in the picker", func(s string) bool { return !strings.Contains(s, "Open Directory") })
	var dir string
	r.do(func() error { dir = r.host.Here().Project; return nil })
	if dir != p.Dir {
		t.Errorf("the window is on %q, want the session's worktree %q", dir, p.Dir)
	}
}

// sessions.md, "direct": with a session host, Open Directory is a direct session with no agent, the same one each
// time, so committing and raising a PR work there as in any session.
func TestOpenDirectoryIsADirectSession(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	open := func() core.SessionState {
		if err := r.do(func() error {
			return r.host.Run(context.Background(), "directory.open", registry.Args{"path": lw.repo})
		}); err != nil {
			t.Fatal(err)
		}
		st := waitState(t, r.store, "a direct session in front", func(st core.State) bool {
			se, ok := st.Session(st.Focus)
			return ok && se.Snapshot().Kind == core.KindDirect
		})
		se, _ := st.Session(st.Focus)
		return se.Snapshot()
	}
	first := open()
	if first.Dir != lw.repo || first.Agent.Provider != "" || len(first.Projects) != 1 || first.Projects[0].Branch != "main" {
		t.Fatalf("direct session = %+v, want the repo itself on main, with no agent", first)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		var dir string
		r.do(func() error { dir = r.host.Here().Project; return nil })
		if dir == lw.repo {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window is on %q, want %q", dir, lw.repo)
		}
	}
	if again := open(); again.ID != first.ID {
		t.Errorf("opening it again started %s, want %s again", again.ID, first.ID)
	}
	if n := len(r.store.State().Sessions); n != 1 {
		t.Errorf("%d sessions, want the one", n)
	}
}

// oriel <dir> opens on the directory's direct session too.
func TestLaunchOnADirectoryIsADirectSession(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), lw.repo, nil, lw.connect(t))
	st := waitState(t, r.store, "a direct session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Kind == core.KindDirect && se.Snapshot().Dir == lw.repo
	})
	if len(st.Sessions) != 1 {
		t.Errorf("sessions = %+v, want the direct one", st.Sessions)
	}
}

// AI-7 layer 2: a Permission hook answers before the user is asked.
func TestPermissionHooksAnswerForTheUser(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", func(h plugin.Host) {
		h.Hook(registry.Hook{Event: "Permission", Kind: registry.Decide, Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			p := ev.(registry.PermissionEvent)
			if p.Tool == "Bash" && strings.HasPrefix(p.Input, "go test ") {
				return registry.Verdict{Decided: true, Value: "allow"}, nil
			}
			return registry.Verdict{}, nil
		}})
	}, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo})
	})
	a := lw.fake.WaitStarted(t)
	in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	select {
	case d := <-a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: in}):
		if !d.Allow {
			t.Errorf("the hook's allow came back as %+v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never answered")
	}
	rm, _ := json.Marshal(map[string]string{"command": "rm -rf build"})
	a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: rm})
	waitState(t, r.store, "a card for what the hook left", func(st core.State) bool {
		se, _ := st.Session("w")
		return se != nil && len(se.Snapshot().Asks) == 1
	})
}

// LS-3's way in: a plugin's agent tool is offered to the agent, and runs in the app for the session that asked.
func TestAgentToolsRunInTheApp(t *testing.T) {
	lw := newLocalHost(t)
	var session string
	r := startAppWith(t, t.TempDir(), "", func(h plugin.Host) {
		h.Contribute(registry.AgentTool{Name: "staging_logs", Desc: "Tail the staging logs", Params: map[string]string{"service": "string", "lines": "number?"},
			ReadOnly: true, Run: func(ctx registry.Ctx, p map[string]any) (any, error) {
				session = registry.SessionOf(ctx)
				return "logs of " + p["service"].(string), nil
			}})
	}, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo})
	})
	a := lw.fake.WaitStarted(t)
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

// AI-2's turns fire the AgentTurnEnd hook, with the session, from Lua or Go.
func TestAgentTurnEndFires(t *testing.T) {
	lw := newLocalHost(t)
	ended := make(chan string, 4)
	r := startAppWith(t, t.TempDir(), "", func(h plugin.Host) {
		h.Hook(registry.Hook{Event: "AgentTurnEnd", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			ended <- ev.(AgentTurnEnd).Session
			return registry.Verdict{}, nil
		}})
	}, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo})
	})
	a := lw.fake.WaitStarted(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	select {
	case got := <-ended:
		if got != "w" {
			t.Errorf("AgentTurnEnd for %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AgentTurnEnd never fired")
	}
}
