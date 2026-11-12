package agentui_test

import (
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/widget"
)

// rulesRig is the app on a session host with the fake agent, and a home of its own to start another host on.
type rulesRig struct {
	t    *testing.T
	lw   *rig.Host
	s    *apptest.Scene
	home string
}

func newRulesRig(t *testing.T, home string) *rulesRig {
	t.Helper()
	lw := rig.NewHostWith(t, host.Options{Home: home})
	return &rulesRig{t: t, lw: lw, s: apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)}), home: home}
}

// start starts a work session named name, in front.
func (r *rulesRig) start(name string) *agenttest.FakeSession {
	r.t.Helper()
	r.s.Run("session.start", registry.Args{"kind": "work", "name": name, "provider": "fake", "repos": r.lw.Repo})
	a := r.lw.Fake.WaitStarted(r.t)
	r.front(name)
	return a
}

func (r *rulesRig) front(name string) {
	r.t.Helper()
	r.s.Run("session.focus", registry.Args{"id": name})
	r.s.Until(name+" in front", func() bool { return r.s.Store().State().Focus == core.SessionID(name) })
}

// goTest asks a's gate for go test args.
func goCmd(a *agenttest.FakeSession, id, cmd string) chan agent.Decision {
	return a.Ask(agent.Permission{ID: id, Tool: "Bash", Summary: cmd, Input: rig.ToolInput(map[string]string{"command": cmd})})
}

// prompted waits for the prompt and answers it with key once its keys count.
func (r *rulesRig) prompted(key string) {
	r.t.Helper()
	r.s.Until("the prompt", func() bool { return r.s.FindRole(render.RoleList, "Answers") != nil })
	r.s.Now = r.s.Now.Add(widget.CardKeyGuard)
	r.s.Frame()
	r.s.Press(key)
}

// recv is the agent's answer on ch, once the app has given it, drawing frames meanwhile.
func (r *rulesRig) recv(what string, ch chan agent.Decision) agent.Decision {
	r.t.Helper()
	var d agent.Decision
	r.s.Until(what, func() bool {
		select {
		case d = <-ch:
			return true
		default:
			return false
		}
	})
	return d
}

// noPrompt says the call went through with no prompt.
func (r *rulesRig) noPrompt(ch chan agent.Decision, what string) {
	r.t.Helper()
	if d := r.recv(what, ch); !d.Allow {
		r.t.Errorf("%s was refused: %+v", what, d)
	}
	r.s.Quiet()
	if r.s.FindRole(render.RoleList, "Answers") != nil {
		r.t.Errorf("%s brought a prompt up", what)
	}
}

// a on a prompt makes a rule for its session: the same call goes through there without one, and still asks in
// another. ⇧A makes one for all sessions: the call goes through in the others too, after a restart as well, until
// it's removed in Settings.
func TestPromptRulesForThisSessionAndAllSessions(t *testing.T) {
	home := t.TempDir()
	r := newRulesRig(t, home)
	one := r.start("one")
	two := r.start("two")

	r.front("one")
	ch := goCmd(one, "p1", "go test ./...")
	r.prompted("a")
	if d := r.recv("the answer", ch); !d.Allow {
		t.Fatalf("a answered %+v", d)
	}
	r.noPrompt(goCmd(one, "p2", "go test -run X ./..."), "go test again in one")

	r.front("two")
	ch = goCmd(two, "p3", "go test ./...")
	r.prompted("<S-a>")
	if d := r.recv("the answer", ch); !d.Allow {
		t.Fatalf("⇧A answered %+v", d)
	}
	ch = goCmd(two, "p4", "go vet ./...")
	r.prompted("<S-a>")
	r.recv("the answer", ch)
	r.front("one")
	r.noPrompt(goCmd(one, "p5", "go vet ./internal/..."), "go vet in one, allowed in two for all sessions")

	// Restarted on the same home, the rules for all sessions are as they were, and Settings lists them.
	if err := r.s.Headless.Close(); err != nil {
		t.Fatal(err)
	}
	r.lw.W.Close()
	r = newRulesRig(t, home)
	three := r.start("three")
	r.noPrompt(goCmd(three, "p6", "go vet ./..."), "go vet in a session after a restart")
	r.s.Run("settings.open", registry.Args{"section": "rules"})
	r.s.WantFrame("Bash(go test:*)", "Bash(go vet:*)", "added ")
	if n := r.s.FindRole(render.RoleGroup, "Bash(go vet:*), permission"); n == nil {
		t.Fatalf("Settings › rules doesn't list go vet's rule:\n%s", r.s.Text())
	}
	r.s.Click(apptest.Centre(r.s.Within("Bash(go vet:*), permission", "Remove").Bounds), 0)
	r.s.Until("the rule removed", func() bool { return len(r.s.Store().State().Rules) == 1 })
	r.s.Run("settings.open", registry.Args{"section": "rules"})
	r.s.Type("Esc")
	r.s.Quiet()
	ch = goCmd(three, "p7", "go vet ./...")
	r.prompted("n")
	if d := r.recv("the answer", ch); d.Allow {
		t.Errorf("removed, yet go vet was allowed: %+v", d)
	}
}
