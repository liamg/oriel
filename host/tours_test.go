package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

func (r *rig) tourSession(variant string) (core.SessionID, *agenttest.FakeSession) {
	r.t.Helper()
	id := r.start(core.SessionSpec{Kind: core.KindTour, Prompt: "How does rounding work?", Variant: variant, Provider: "fake",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	return id, r.agent()
}

// toolErr calls a tool that should fail, and says why it did.
func toolErr(t *testing.T, a *agenttest.FakeSession, name string, args any) string {
	t.Helper()
	b, _ := json.Marshal(args)
	for _, tool := range a.Spec.Tools {
		if tool.Name == name {
			out, err := tool.Call(context.Background(), b)
			if err == nil {
				t.Fatalf("%s succeeded: %q", name, out)
			}
			return err.Error()
		}
	}
	t.Fatalf("no tool %s", name)
	return ""
}

// sessions.md: a tour starts from "a question", and its kind is one of crit's seven ("tour (7 kinds plus export,
// internal/tour)", crit-reference.md); the launcher offers them as the tour kind's variants.
func TestTourKindsAreVariants(t *testing.T) {
	r := newRig(t)
	k, _ := r.w.kinds.Get(core.KindTour)
	if len(k.Variants) != 7 || k.Variants[0].ID != "walkthrough" || k.Variants[4].ID != "demo" || k.Variants[4].Desc == "" {
		t.Fatalf("variants = %+v", k.Variants)
	}
	id := r.start(core.SessionSpec{Kind: core.KindTour, Prompt: "How?", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if m, _ := LoadManifest(r.session(id).Dir); m.Tour == nil || m.Tour.Kind != "walkthrough" {
		t.Errorf("a tour with no kind asked for is %+v", m.Tour)
	}
	if _, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindTour, Prompt: "How?", Variant: "nonsense"}); err == nil ||
		!strings.Contains(err.Error(), "walkthrough, onboarding") {
		t.Errorf("a nonsense kind of tour: %v", err)
	}
	if _, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindScratch, Variant: "demo"}); err == nil {
		t.Error("a scratch session took a variant")
	}
}

// crit's tour skill: "the question is waiting here as the first comment on the where="tour" thread", and the
// exports are kept out of the workspace's own git.
func TestTourQuestionIsTheTourThread(t *testing.T) {
	r := newRig(t)
	id, a := r.tourSession("")
	first := a.WaitSent(t)
	if !strings.Contains(first.Text, `where="tour"`) || !strings.Contains(first.Text, "How does rounding work?") {
		t.Errorf("first batch = %q", first.Text)
	}
	b, _ := os.ReadFile(filepath.Join(r.session(id).Dir, ".git", "info", "exclude"))
	if !strings.Contains(string(b), "tour.html\n") || !strings.Contains(string(b), "tour.md\n") {
		t.Errorf("exclude = %q", b)
	}
}

// crit's tourcmd.go as tools: tour-kind prints the kind's guidance (and sets it), tour-steps lists the steps,
// tour-run records a command in a step, tour-share records the link.
func TestTourTools(t *testing.T) {
	r := newRig(t)
	id, a := r.tourSession("walkthrough")
	dir := r.session(id).Dir

	got := a.Call(t, "tour_kind", map[string]any{})
	if !strings.HasPrefix(got, "This tour is a walkthrough tour. Kinds: walkthrough, onboarding, archaeology, showcase, demo, incident, feasibility.") ||
		!strings.Contains(got, "How to shape a walkthrough tour:\n\nThe default. Entry point down") {
		t.Errorf("tour_kind = %q", got)
	}
	got = a.Call(t, "tour_kind", map[string]any{"kind": "archaeology"})
	if !strings.HasPrefix(got, "This tour is now an archaeology tour.") || !strings.Contains(got, "git log -L") {
		t.Errorf("tour_kind archaeology = %q", got)
	}
	if info, _ := TourOf(dir); info.Kind != "archaeology" || info.Title != "How does rounding work?" || len(info.Repos) != 1 || info.Repos[0].Commit == "" {
		t.Errorf("tour = %+v", info)
	}
	if e := toolErr(t, a, "tour_kind", map[string]any{"kind": "nonsense"}); !strings.Contains(e, "isn't a kind of tour") {
		t.Errorf("a nonsense kind: %s", e)
	}

	if got := a.Call(t, "tour_steps", nil); !strings.Contains(got, "No steps yet") {
		t.Errorf("tour_steps = %q", got)
	}
	os.WriteFile(filepath.Join(dir, "02-b.md"), []byte("# B\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "01-a.md"), []byte("# A\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "NOTES.md"), nil, 0o644)
	if got := a.Call(t, "tour_steps", nil); got != " 1  01-a.md\n 2  02-b.md" {
		t.Errorf("tour_steps = %q", got)
	}

	got = a.Call(t, "tour_run", map[string]any{"command": []string{"sh", "-c", "echo rounded; exit 2"}, "step": "2", "cwd": "repos/" + filepath.Base(r.repo.Dir)})
	if got != "Recorded in step 02-b.md (exit 2, 8 bytes of output)." {
		t.Errorf("tour_run = %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "02-b.md"))
	if !strings.Contains(string(b), "# B\n\n```run cwd=repos/") || !strings.Contains(string(b), "exit=2 at=2026-") || !strings.Contains(string(b), "\nrounded\n```\n") {
		t.Errorf("step 2 = %q", b)
	}
	if got := a.Call(t, "tour_run", map[string]any{"command": []string{"echo", "hi"}}); !strings.HasPrefix(got, "Not recorded") || !strings.Contains(got, "hi") {
		t.Errorf("tour_run without a step = %q", got)
	}
	if e := toolErr(t, a, "tour_run", map[string]any{"command": []string{"echo"}, "cwd": "../.."}); !strings.Contains(e, "inside the tour") {
		t.Errorf("a cwd outside the tour: %s", e)
	}

	if got := a.Call(t, "tour_share", map[string]any{"url": "https://claude.ai/artifact/abc"}); got != "Shared: https://claude.ai/artifact/abc" {
		t.Errorf("tour_share = %q", got)
	}
	if info, _ := TourOf(dir); info.ShareURL != "https://claude.ai/artifact/abc" || !info.SharedAt.Equal(r.now()) {
		t.Errorf("shared = %+v", info)
	}
	if e := toolErr(t, a, "tour_share", map[string]any{"url": "javascript:alert(1)"}); !strings.Contains(e, "web address") {
		t.Errorf("a javascript: share: %s", e)
	}
}

func TestTourToolsRefuseOtherSessions(t *testing.T) {
	r := newRig(t)
	_, a := r.workSession("Work")
	if e := toolErr(t, a, "tour_steps", nil); e != "this session isn't a tour" {
		t.Errorf("tour_steps in a work session: %s", e)
	}
}

// A sandboxed agent's commands run in its sandbox, never on the host for it.
func TestTourRunRefusedInASandbox(t *testing.T) {
	r := newRig(t)
	id, a := r.tourSession("demo")
	se := r.session(id)
	se.Agent.Sandboxed = true
	if err := r.w.emit(core.SessionSynced{Session: se}); err != nil {
		t.Fatal(err)
	}
	if e := toolErr(t, a, "tour_run", map[string]any{"command": []string{"echo"}}); !strings.Contains(e, "sandbox") {
		t.Errorf("tour_run in a sandbox: %s", e)
	}
}
