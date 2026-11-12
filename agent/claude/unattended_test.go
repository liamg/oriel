package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/eventq"
)

// An unattended agent's permission and question, when nobody answers them, are refused with a reason after a short
// wait rather than holding its turn; an attended one's wait for their answer.
func TestUnattendedAsksAreRefusedWhenNobodyAnswers(t *testing.T) {
	newSession := func(unattended bool) *session {
		return &session{spec: agent.StartSpec{Unattended: unattended}, events: eventq.New(), asks: map[string]chan agent.Decision{},
			unattendedWait: 20 * time.Millisecond}
	}
	ctx := context.Background()

	s := newSession(true)
	perm := s.permission(ctx, permissionIn{ID: "p1", Tool: "Bash"})
	if perm.Decision != "deny" || !strings.Contains(perm.Message, "Nobody") {
		t.Errorf("the permission: %+v", perm)
	}
	if !s.spec.Unattended || len(s.asks) != 0 {
		t.Errorf("a refused ask is still waiting: %v", s.asks)
	}
	q := s.question(ctx, questionIn{ID: "q1"})
	if !strings.Contains(q.Deny, "Nobody") {
		t.Errorf("the question: %+v", q)
	}

	s = newSession(false)
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if perm := s.permission(short, permissionIn{ID: "p2", Tool: "Bash"}); perm.Decision != "" {
		t.Errorf("an attended permission was answered without its user: %+v", perm)
	}
}

// The mod refuses an unattended agent's dialogs with the same words the host does, and only when Oriel set it so.
func TestModRefusesUnattendedDialogs(t *testing.T) {
	ts, err := mod.ReadFile("mod/hooks/register.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'" + agent.UnattendedRefusal + "'", "ORIEL_UNATTENDED"} {
		if !strings.Contains(string(ts), want) {
			t.Errorf("register.ts lacks %s", want)
		}
	}
}

// The PermissionRequest settings hook answers as the mod's hook did: Allow always hands back the call's suggestions
// as the rules to keep, a deny its words; with no answer the dialog opens, unless nobody is there to answer it.
func TestThePermissionHookAnswers(t *testing.T) {
	newSession := func(unattended bool) *session {
		return &session{spec: agent.StartSpec{Unattended: unattended}, events: eventq.New(), asks: map[string]chan agent.Decision{}}
	}
	sugg := json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash"}],"behavior":"allow","destination":"localSettings"}]`)
	in := classicIn{Event: "PermissionRequest", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`), Suggestions: sugg}
	answered := func(s *session, d agent.Decision) any {
		out := make(chan any, 1)
		go func() { out <- s.classic(context.Background(), in) }()
		for ev := range s.events.Out() {
			if p, ok := ev.(agent.Permission); ok {
				if err := s.Answer(context.Background(), p.ID, d); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		return <-out
	}
	r, ok := answered(newSession(false), agent.Decision{Allow: true, Always: true}).(permissionHookOut)
	if !ok || r.Specific.Event != "PermissionRequest" || r.Specific.Decision.Behavior != "allow" || string(r.Specific.Decision.UpdatedPermissions) != string(sugg) {
		t.Errorf("allowed always: %+v", r)
	}
	r, ok = answered(newSession(false), agent.Decision{Note: "not now"}).(permissionHookOut)
	if !ok || r.Specific.Decision.Behavior != "deny" || r.Specific.Decision.Message != "Denied by the user: not now" {
		t.Errorf("denied: %+v", r)
	}

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if out := newSession(false).classic(gone, in); out != (struct{}{}) {
		t.Errorf("unanswered: %+v, want nothing (the dialog)", out)
	}
	if r, ok := newSession(true).classic(gone, in).(permissionHookOut); !ok || r.Specific.Decision.Behavior != "deny" || r.Specific.Decision.Message != agent.UnattendedRefusal {
		t.Errorf("unanswered, unattended: %+v", r)
	}
	if out := newSession(false).classic(gone, classicIn{Event: "PermissionRequest", Tool: "AskUserQuestion"}); out != (struct{}{}) {
		t.Errorf("the agent's question: %+v, want it left to the mod", out)
	}
}
