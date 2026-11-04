package app_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// asstCall runs one of the tools the assistant's agent was offered, as the agent would, off the loop; the result
// comes on the channel while the test frames.
func asstCall(a *agenttest.FakeSession, name string, args any) chan [2]any {
	done := make(chan [2]any, 1)
	go func() {
		out, err := a.CallErr(name, args)
		done <- [2]any{out, err}
	}()
	return done
}

// settleCall frames until the call answers.
func settleCall(s *apptest.Scene, t *testing.T, done chan [2]any) (string, error) {
	t.Helper()
	var r [2]any
	s.Until("the tool's answer", func() bool {
		select {
		case r = <-done:
			return true
		default:
			return false
		}
	})
	err, _ := r[1].(error)
	return r[0].(string), err
}

func toolNames(a *agenttest.FakeSession) []string {
	var out []string
	for _, t := range a.Spec.Tools {
		out = append(out, t.Name)
	}
	return out
}

// "How do I go back to the help page I was reading?": help_search finds the section, help_read gives it with its keys
// as they're bound now, and open_help shows it.
func TestAssistantSearchesTheHelp(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = time.Date(2026, 10, 15, 14, 5, 0, 0, time.UTC)
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Frame()
	s.Run("assistant.open", registry.Args{"text": "how do I go back to the help page I was reading?"})
	ua := lw.Fake.WaitStarted(t)
	for _, want := range []string{"help_search", "help_read", "open_help"} {
		if !slices.Contains(toolNames(ua), want) {
			t.Errorf("no %s for the assistant", want)
		}
	}
	out, err := settleCall(s, t, asstCall(ua, "help_search", map[string]any{"query": "how do I go back to the help pages I've read?"}))
	if err != nil {
		t.Fatal(err)
	}
	var hits []struct{ ID, Title, Snippet string }
	if json.Unmarshal([]byte(out), &hits) != nil || len(hits) == 0 || hits[0].ID != "shell/help" || !strings.Contains(hits[0].Snippet, "back and forward") {
		t.Fatalf("help_search: %s", out)
	}
	if _, err := settleCall(s, t, asstCall(ua, "help_search", map[string]any{"query": "xyzzy plugh"})); err == nil {
		t.Error("a search for nothing found something")
	}
	out, err = settleCall(s, t, asstCall(ua, "help_read", map[string]any{"id": "shell/help"}))
	if err != nil {
		t.Fatal(err)
	}
	var sec struct{ ID, Title, Text string }
	if json.Unmarshal([]byte(out), &sec) != nil || sec.Title != "Help" || !strings.Contains(sec.Text, "Keys:\n") ||
		!strings.Contains(sec.Text, "(help mode): Back") {
		t.Fatalf("help_read: %s", out)
	}
	if _, err := settleCall(s, t, asstCall(ua, "open_help", map[string]any{"id": "shell/help"})); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	if s.FindRole(render.RoleDialog, "Help: Help") == nil {
		t.Errorf("open_help didn't open the help on shell/help:\n%s", s.Text())
	}
}
