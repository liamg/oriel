package app

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// localWorker is a worker in the test's process, with the fake provider and a fixture repo, connected the way the
// app connects to this machine's worker.
type localWorker struct {
	w    *worker.Worker
	fake *agenttest.Fake
	repo string
}

// newLocalWorker is a worker with the fake provider, or the fakes given (named as the providers they stand for).
func newLocalWorker(t *testing.T, fakes ...*agenttest.Fake) *localWorker {
	t.Helper()
	return newWorkerWith(t, worker.Options{}, fakes...)
}

// newWorkerWith is newLocalWorker with options of its own: another machine's ID and name, its repos directory.
func newWorkerWith(t *testing.T, o worker.Options, fakes ...*agenttest.Fake) *localWorker {
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
	lw := &localWorker{fake: agenttest.NewFake(), repo: repo}
	providers := &agent.Providers{}
	if len(fakes) == 0 {
		fakes = []*agenttest.Fake{lw.fake}
	}
	for _, f := range fakes {
		providers.Register(f)
	}
	o.Home, o.Providers, o.Exec = t.TempDir(), providers, nopExec{}
	o.Packs = func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }
	w, err := worker.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	lw.w = w
	return lw
}

type nopExec struct{ agent.Exec }

// connect is a Config change that connects the app to the worker.
func (lw *localWorker) connect(t *testing.T) func(*Config) {
	return func(cfg *Config) {
		a, b := wire.Pipe()
		lw.w.Serve(b)
		t.Cleanup(func() { a.Close(); b.Close() })
		cfg.Workers = append(cfg.Workers, worker.Client{Conn: a})
	}
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

// AI-1: the app mirrors the worker's sessions, starts one through a command, and the window follows it.
func TestTheWindowFollowsTheSessionInFront(t *testing.T) {
	lw := newLocalWorker(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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

// AI-7 layer 2: a Permission hook answers before the user is asked.
func TestPermissionHooksAnswerForTheUser(t *testing.T) {
	lw := newLocalWorker(t)
	r := startAppWith(t, t.TempDir(), "", func(h ext.Host) {
		h.Hook(registry.Hook{Event: "Permission", Kind: registry.Decide, Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			p := ev.(registry.PermissionEvent)
			if p.Tool == "Bash" && strings.HasPrefix(p.Input, "go test ") {
				return registry.Verdict{Decided: true, Value: "allow"}, nil
			}
			return registry.Verdict{}, nil
		}})
	}, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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
	lw := newLocalWorker(t)
	var session string
	r := startAppWith(t, t.TempDir(), "", func(h ext.Host) {
		h.Contribute(registry.AgentTool{Name: "staging_logs", Desc: "Tail the staging logs", Params: map[string]string{"service": "string", "lines": "number?"},
			ReadOnly: true, Run: func(ctx registry.Ctx, p map[string]any) (any, error) {
				session = registry.SessionOf(ctx)
				return "logs of " + p["service"].(string), nil
			}})
	}, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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
	lw := newLocalWorker(t)
	ended := make(chan string, 4)
	r := startAppWith(t, t.TempDir(), "", func(h ext.Host) {
		h.Hook(registry.Hook{Event: "AgentTurnEnd", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			ended <- ev.(AgentTurnEnd).Session
			return registry.Verdict{}, nil
		}})
	}, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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
