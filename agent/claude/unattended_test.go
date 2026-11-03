package claude

import (
	"context"
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
