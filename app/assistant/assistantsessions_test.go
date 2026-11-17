package assistant_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/registry"
)

// twoSessions is the assistant's scene with two work sessions, "first" and "second" (in front), and the assistant's
// agent asked something.
func twoSessions(t *testing.T) (*apptest.Scene, *rig.Host, *agentCalls) {
	t.Helper()
	s, lw, _ := assistantScene(t, nil, nil, nil)
	for _, name := range []string{"first", "second"} {
		s.Run("session.start", registry.Args{"kind": "work", "name": name, "provider": "fake", "repos": []string{lw.Repo}})
		lw.Fake.WaitStarted(t)
	}
	s.UntilState("second in front", func(st core.State) bool { return apptest.FocusedOn(st, "second") })
	s.Run("assistant.open", registry.Args{"text": "tidy my sessions"})
	s.Frame()
	widenAssistant(t, s)
	return s, lw, &agentCalls{t: t, s: s, a: lw.Fake.WaitStarted(t)}
}

// "Open first", "call it pricing cache", undo that: open_session brings a session to the front by name, and
// rename_session renames it with an undo that puts the old name back.
func TestAssistantOpensAndRenamesSessions(t *testing.T) {
	s, _, ua := twoSessions(t)
	first := apptest.Named(s.Store().State(), "first")

	ua.ok("open_session", map[string]any{"id": "first"})
	s.UntilState("first in front", func(st core.State) bool { return st.Focus == first })
	s.Until("the action", func() bool { return strings.Contains(s.Text(), "✓ opened first") })

	ua.ok("rename_session", map[string]any{"id": string(first), "name": "pricing cache"})
	s.UntilState("the new name", func(st core.State) bool { se, _ := st.Session(first); return se.Name() == "pricing cache" })
	s.Until("the rename", func() bool { return strings.Contains(s.Text(), "✓ renamed first to pricing cache") })
	s.ClickTop("Undo 10s")
	s.UntilState("the old name back", func(st core.State) bool { se, _ := st.Session(first); return se.Name() == "first" })

	ua.fails("rename_session", map[string]any{"id": string(first), "name": "  "}, "what should it be called?")
	ua.fails("open_session", map[string]any{"id": "nope"}, `no session "nope"`)
}

// "Stop first", then "bring it back": stop_session asks first, showing what stops, and the stopped session is
// among past_sessions, which resume_session brings back; a running one isn't resumed.
func TestAssistantStopsAndResumesASession(t *testing.T) {
	s, lw, ua := twoSessions(t)
	first := apptest.Named(s.Store().State(), "first")

	done := asstCall(ua.a, "stop_session", map[string]any{"id": "first"})
	s.Until("the card", func() bool { return strings.Contains(s.Text(), "Stop a session") })
	s.WantFrame("The assistant wants to stop this session and its agent:", "session", "first", "repositories", "pricing",
		"ITS UNFINISHED TASKS", "none", "Its workspace stays, and the session can be resumed later.")
	if se, _ := s.Store().State().Session(first); !se.Agent().Live() {
		t.Fatal("the agent stopped before the yes")
	}
	s.Type("y")
	if _, err := settleCall(t, s, done); err != nil {
		t.Fatal(err)
	}
	s.UntilState("first stopped", func(st core.State) bool { se, _ := st.Session(first); return !se.Agent().Live() })

	var past []struct{ ID, Name, Since string }
	if out := ua.ok("past_sessions", map[string]any{}); json.Unmarshal([]byte(out), &past) != nil || len(past) != 1 ||
		past[0].ID != string(first) || past[0].Since == "" {
		t.Fatalf("past_sessions: %s", out)
	}

	ua.fails("resume_session", map[string]any{"id": "second"}, "second is running already: open_session opens it")
	ua.ok("resume_session", map[string]any{"id": string(first)})
	lw.Fake.WaitStarted(t)
	s.UntilState("first back and in front", func(st core.State) bool {
		se, _ := st.Session(first)
		return se.Agent().Live() && st.Focus == first
	})
	if out := ua.ok("past_sessions", map[string]any{}); out != "[]" {
		t.Errorf("past_sessions after the resume: %s", out)
	}
}

// "Start me a work session on pricing" and "show me the git settings": new_session opens the launcher filled in for
// the user to finish, and settings opens the settings page.
func TestAssistantOpensTheLauncherAndSettings(t *testing.T) {
	s, _, ua := twoSessions(t)
	ua.ok("new_session", map[string]any{"kind": "work", "prompt": "Cache the price lookups", "repos": "pricing"})
	s.Until("the launcher", func() bool { return strings.Contains(s.Text(), "Cache the price lookups") })
	if n := len(s.Store().State().Sessions); n != 2 {
		t.Errorf("the launcher started a session itself: %d sessions", n)
	}
	s.Press("<Esc>")

	ua.ok("settings", map[string]any{"section": "git"})
	s.Until("the settings page", func() bool { return s.Shown("settings") })
}

// "What repos do I have, what jobs, how's my usage, run the deps sweep": repos and jobs list what's registered, usage
// reads each plan's windows, and run_job asks first, saying how the last run went, then runs it in a session.
func TestAssistantReadsReposJobsAndUsage(t *testing.T) {
	s, lw, ua := twoSessions(t)
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	var repos []struct{ Name, Path string }
	if out := ua.ok("repos", map[string]any{}); json.Unmarshal([]byte(out), &repos) != nil || len(repos) != 1 ||
		repos[0].Name != "pricing" || repos[0].Path != lw.Repo {
		t.Fatalf("repos: %s", out)
	}

	if out := ua.ok("jobs", map[string]any{}); out != "[]" {
		t.Fatalf("jobs before any is saved: %s", out)
	}
	j, err := lw.W.SaveJob(hostapi.Job{Title: "Sweep stale deps", Prompt: "Bump every dependency that's behind.", Sandbox: "host", Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	var jobs []struct{ Name, Title, LastRun string }
	if out := ua.ok("jobs", map[string]any{}); json.Unmarshal([]byte(out), &jobs) != nil || len(jobs) != 1 ||
		jobs[0].Name != j.Name || jobs[0].Title != "Sweep stale deps" || jobs[0].LastRun != "" {
		t.Fatalf("jobs: %s", out)
	}
	ua.fails("run_job", map[string]any{"name": "nope"}, "no job called nope")
	done := asstCall(ua.a, "run_job", map[string]any{"name": "sweep stale deps"})
	s.Until("the card", func() bool { return strings.Contains(s.Text(), "Run a job") })
	s.WantFrame("The assistant wants to run this saved job now, in a fresh workspace:", "Sweep stale deps", "never run")
	s.Type("y")
	out, err := settleCall(t, s, done)
	if err != nil || !strings.Contains(out, `"session"`) {
		t.Fatalf("run_job: %q %v", out, err)
	}
	lw.Fake.WaitStarted(t)
	s.UntilState("the job's session", func(st core.State) bool {
		for _, se := range st.Sessions {
			if hostapi.JobOf(se) == j.Name {
				return true
			}
		}
		return false
	})

	if err := s.Store().Apply(rig.ClaudeMaxUsage); err != nil {
		t.Fatal(err)
	}
	var usage struct {
		Plans []struct {
			Provider, Plan string
			Windows        []struct{ Name, Used string }
		}
	}
	if out := ua.ok("usage", map[string]any{}); json.Unmarshal([]byte(out), &usage) != nil || len(usage.Plans) != 1 ||
		usage.Plans[0].Plan != "Claude Max" || len(usage.Plans[0].Windows) == 0 || !strings.HasSuffix(usage.Plans[0].Windows[0].Used, "%") {
		t.Fatalf("usage: %s", out)
	}
}

// agentCalls calls the assistant's tools as its agent would, framing while each runs.
type agentCalls struct {
	t *testing.T
	s *apptest.Scene
	a *agenttest.FakeSession
}

func (c *agentCalls) ok(name string, args map[string]any) string {
	c.t.Helper()
	out, err := settleCall(c.t, c.s, asstCall(c.a, name, args))
	if err != nil {
		c.t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (c *agentCalls) fails(name string, args map[string]any, want string) {
	c.t.Helper()
	if _, err := settleCall(c.t, c.s, asstCall(c.a, name, args)); err == nil || !strings.Contains(err.Error(), want) {
		c.t.Errorf("%s %v: %v, want an error saying %q", name, args, err, want)
	}
}
