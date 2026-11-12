package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// connectorHarness is the fake's harness with spec changed.
func connectorHarness(t *testing.T, change func(*agent.StartSpec)) agenttest.Harness {
	h := fakeHarness(t)
	spec := h.Spec
	h.Spec = func(t *testing.T, dir string) agent.StartSpec {
		s := spec(t, dir)
		change(&s)
		return s
	}
	return h
}

// Every MCP call asks Oriel, whatever Claude's own mode would do with it: a connector's call raises Oriel's
// permission, which the user answers, on the host's gate and in a sandbox's bypassPermissions alike.
func TestAConnectorCallAsksOriel(t *testing.T) {
	for name, change := range map[string]func(*agent.StartSpec){
		"asking":    func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk },
		"light":     func(s *agent.StartSpec) { s.Policy = agent.PolicyLight },
		"sandboxed": func(s *agent.StartSpec) { s.Sandbox = &agent.Sandbox{Name: "box"}; s.Policy = agent.PolicyBypass },
	} {
		t.Run(name, func(t *testing.T) {
			s := agenttest.Start(t, connectorHarness(t, change))
			s.Send("call a connector")
			p := s.Wait("the permission", func(ev agent.Event) bool { _, ok := ev.(agent.Permission); return ok }).(agent.Permission)
			if p.Tool != fakeConnector || p.Rule != fakeConnector {
				t.Errorf("permission = %+v", p)
			}
			if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Allow: true}); err != nil {
				t.Fatal(err)
			}
			if got := s.Reply(); !strings.Contains(got, "ran: the index") {
				t.Errorf("allowed, the reply is %q", got)
			}
			s.Send("call a connector")
			p = s.Wait("the second permission", func(ev agent.Event) bool { q, ok := ev.(agent.Permission); return ok && q.ID != p.ID }).(agent.Permission)
			if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Note: "not now"}); err != nil {
				t.Fatal(err)
			}
			if got := s.Reply(); !strings.Contains(got, "denied: Denied by the user: not now") {
				t.Errorf("denied, the reply is %q", got)
			}
		})
	}
}

// Oriel's own tools never ask: they run without a permission, whatever the gate.
func TestOrielsOwnToolsNeverAsk(t *testing.T) {
	s := agenttest.Start(t, connectorHarness(t, func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk }))
	s.Send("call echo")
	if got := s.Reply(); !strings.Contains(got, "echo: hi") {
		t.Errorf("reply = %q", got)
	}
	for _, ev := range s.Events {
		if p, ok := ev.(agent.Permission); ok {
			t.Errorf("Oriel's own tool asked: %+v", p)
		}
	}
}

// A utility agent has nobody to ask: Oriel answers its MCP calls in the check, at once. What it was allowed runs,
// and the rest is refused with a reason, neither raising a permission.
func TestAUtilityAgentsMCPCallsAreAnsweredAtOnce(t *testing.T) {
	utility := func(allow ...string) func(*agent.StartSpec) {
		return func(s *agent.StartSpec) { s.Policy, s.Unattended, s.Allow = agent.PolicyRefuse, true, allow }
	}
	s := agenttest.Start(t, connectorHarness(t, utility("WebSearch")))
	s.Send("call a connector")
	if got := s.Reply(); !strings.Contains(got, "denied: Oriel: nobody is here to approve "+fakeConnector) {
		t.Errorf("outside its Allow, the reply is %q", got)
	}
	s.Send("call echo")
	if got := s.Reply(); !strings.Contains(got, "echo: hi") {
		t.Errorf("its own tool: %q", got)
	}
	a := agenttest.Start(t, connectorHarness(t, utility(fakeConnector)))
	a.Send("call a connector")
	if got := a.Reply(); !strings.Contains(got, "ran: the index") {
		t.Errorf("in its Allow, the reply is %q", got)
	}
	for _, ev := range slices.Concat(s.Events, a.Events) {
		if p, ok := ev.(agent.Permission); ok {
			t.Errorf("a utility agent asked: %+v", p)
		}
	}
}

// A sandboxed session keeps claude.ai's connectors: their calls ask Oriel like any MCP call, so nothing needs them
// turned off. A session started without MCP servers still has none.
func TestASandboxKeepsClaudeAIConnectors(t *testing.T) {
	const off = "ENABLE_CLAUDEAI_MCP_SERVERS=false"
	if env := processEnv(agent.StartSpec{Sandbox: &agent.Sandbox{Name: "box"}}, "/s", "/oriel"); slices.Contains(env, off) {
		t.Errorf("a sandbox turned the connectors off: %q", env)
	}
	if env := processEnv(agent.StartSpec{NoMCP: true}, "/s", "/oriel"); !slices.Contains(env, off) {
		t.Errorf("a session without MCP servers kept the connectors: %q", env)
	}
}

// unreachable is an oriel whose check never answers, as when the mod can't reach the host: every other op is the
// real one (the test binary).
func unreachable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "oriel")
	script := "#!/bin/sh\nif [ \"$1\" = hook ] && [ \"$2\" = check ]; then echo 'no Oriel here' >&2; exit 1; fi\nexec '" + exe + "' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// An MCP call Oriel couldn't be asked about is refused, never left to Claude's mode: in bypassPermissions it would
// run unasked. Oriel's own tools still run. The fake plays the mod's side; the mod says it in the same words.
func TestAnMCPCallIsRefusedWhenOrielCantBeAsked(t *testing.T) {
	if ts, err := mod.ReadFile("mod/hooks/register.ts"); err != nil || !strings.Contains(string(ts), `"`+CheckFailed+`"`) {
		t.Errorf("register.ts lacks %q (%v)", CheckFailed, err)
	}
	for name, change := range map[string]func(*agent.StartSpec){
		"asking":    func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk },
		"sandboxed": func(s *agent.StartSpec) { s.Sandbox = &agent.Sandbox{Name: "box"}; s.Policy = agent.PolicyBypass },
	} {
		t.Run(name, func(t *testing.T) {
			h := connectorHarness(t, change)
			h.Provider.(*Provider).OrielBin = unreachable(t)
			s := agenttest.Start(t, h)
			s.Send("call a connector")
			if got := s.Reply(); !strings.Contains(got, "denied: "+CheckFailed) {
				t.Errorf("the reply is %q", got)
			}
			s.Send("call echo")
			if got := s.Reply(); !strings.Contains(got, "echo: hi") {
				t.Errorf("Oriel's own tool: %q", got)
			}
			for _, ev := range s.Events {
				if p, ok := ev.(agent.Permission); ok {
					t.Errorf("asked: %+v", p)
				}
			}
		})
	}
}

// Where a managed plugin continues past the mod's PermissionRequest hook, only Claude's own dialog answers a call:
// the mod says so, and the session raises Dialog with the call as its title, no Permission, until the call runs.
func TestACallOnlyClaudesDialogAnswersRaisesDialog(t *testing.T) {
	s := agenttest.Start(t, connectorHarness(t, func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk }))
	s.Send("call a connector past the org's plugin")
	d := s.Wait("the dialog", func(ev agent.Event) bool { d, ok := ev.(agent.Dialog); return ok && d.Shown }).(agent.Dialog)
	if !strings.HasPrefix(d.Title, "Allow "+fakeConnector) || !strings.HasSuffix(d.Title, "?") {
		t.Errorf("the dialog's title is %q", d.Title)
	}
	if _, err := s.S.Terminal().Write([]byte("1\n")); err != nil {
		t.Fatal(err)
	}
	s.Wait("the dialog gone", func(ev agent.Event) bool { d, ok := ev.(agent.Dialog); return ok && !d.Shown })
	if got := s.Reply(); !strings.Contains(got, "ran: the index") {
		t.Errorf("the reply is %q", got)
	}
	for _, ev := range s.Events {
		if p, ok := ev.(agent.Permission); ok {
			t.Errorf("Oriel raised a permission Claude's dialog answers: %+v", p)
		}
	}
}

// A permission the mod hands Oriel takes the place of a dialog it said was up (it had learnt its hook was skipped,
// and is reached after all): the dialog goes as the permission comes.
func TestAPermissionTakesTheDialogsPlace(t *testing.T) {
	s := agenttest.Start(t, connectorHarness(t, func(s *agent.StartSpec) { s.Policy = agent.PolicyAsk }))
	s.Send("call a connector, its dialog told first")
	p := s.Wait("the permission", func(ev agent.Event) bool { _, ok := ev.(agent.Permission); return ok }).(agent.Permission)
	var shown, gone bool
	for _, ev := range s.Events {
		if d, ok := ev.(agent.Dialog); ok {
			shown, gone = shown || d.Shown, shown && !d.Shown
		}
	}
	if !shown || !gone {
		t.Errorf("before the permission, the dialog was shown %v and gone %v", shown, gone)
	}
	if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	if got := s.Reply(); !strings.Contains(got, "ran: the index") {
		t.Errorf("the reply is %q", got)
	}
}
