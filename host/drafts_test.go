package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// draftsRig is a work session whose pack drafts (it offers commit_draft), past its first turn, and its worktree.
func draftsRig(t *testing.T) (*rig, core.SessionID, *agenttest.FakeSession, string) {
	t.Helper()
	r := newRig(t)
	r.tools = []string{"reply", "commit_draft", "pr_draft"}
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	handBack(r, id, a)
	p, _ := r.session(id).Primary()
	return r, id, a, p.Dir
}

// handBack resolves the session's request, so the agent owes nothing.
func handBack(r *rig, id core.SessionID, a *agenttest.FakeSession) {
	r.t.Helper()
	a.Call(r.t, "reply", map[string]any{"thread": string(r.session(id).Threads[0].ID), "body": "Done.", "resolve": true})
}

// turn is one turn of the agent's, ending idle.
func turn(r *rig, id core.SessionID, a *agenttest.FakeSession) {
	r.t.Helper()
	n := r.session(id).Agent.Stats.Turns
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn's end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == n+1 })
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit and PR drafts are written when the agent goes idle; the pack's rule (parts/drafts.md):
// "Draft only what exists: no commit message for a clean worktree, no PR draft for a branch with no commits of its
// own and nothing uncommitted", "Re-draft only when the code changed since your last draft". A turn that ends idle
// with a draft missing or behind the code asks the agent once for that code, as a notice and not a thread.
func TestDraftsNudgeOncePerChangeWhenIdle(t *testing.T) {
	r, id, a, dir := draftsRig(t)
	ctx := context.Background()

	turn(r, id, a)
	a.NoneSent(t) // clean, and no commits of its own

	write(t, dir, "round.go", "package pricing\n\nfunc Round(x float64) float64 { return x }\n")
	turn(r, id, a)
	got := a.WaitSent(t)
	for _, want := range []string{NoticeLead, "<notice>", "commit message (mcp__oriel__commit_draft)", "PR draft (mcp__oriel__pr_draft)"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the nudge lacks %q:\n%s", want, got.Text)
		}
	}
	if strings.Contains(got.Text, BatchLead) || len(r.session(id).Threads) != 1 {
		t.Errorf("the nudge is a user thread: %q, %d threads", got.Text, len(r.session(id).Threads))
	}

	// It doesn't draft; the same code isn't asked about again.
	turn(r, id, a)
	a.NoneSent(t)
	turn(r, id, a)
	a.NoneSent(t)

	// The code moves on: asked again. It drafts, and isn't asked again.
	write(t, dir, "round.go", "package pricing\n\nfunc Round(x float64) float64 { return -x }\n")
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	if got := a.WaitSent(t); !strings.Contains(got.Text, "mcp__oriel__commit_draft") {
		t.Fatalf("after the code moved: %q", got.Text)
	}
	a.Emit(agent.TurnStart{})
	a.Call(t, "commit_draft", map[string]any{"message": "fix(pricing): round negatives"})
	a.Call(t, "pr_draft", map[string]any{"title": "fix(pricing): round negatives", "body": "Rounds them."})
	a.Emit(agent.TurnEnd{})
	a.NoneSent(t)
	s := r.ship(id, false)
	for _, p := range s {
		if p.CommitDraft.Stale || p.PRDraft.Stale || p.CommitDraft.By != "fake" {
			t.Errorf("fresh drafts: %+v / %+v", p.CommitDraft, p.PRDraft)
		}
	}

	// Edited since: the drafts are stale, and it's asked to draft both again.
	write(t, dir, "more.go", "package pricing\n")
	for _, p := range r.ship(id, false) {
		if !p.CommitDraft.Stale || !p.PRDraft.Stale {
			t.Errorf("drafts behind the code aren't stale: %+v / %+v", p.CommitDraft, p.PRDraft)
		}
	}
	turn(r, id, a)
	if got := a.WaitSent(t); !strings.Contains(got.Text, "commit_draft") || !strings.Contains(got.Text, "pr_draft") {
		t.Fatalf("stale drafts: %q", got.Text)
	}

	// Committed: no commit message is due, and the PR draft, written for the same change, still holds.
	a.Emit(agent.TurnStart{})
	a.Call(t, "commit_draft", map[string]any{"message": "fix(pricing): round negatives"})
	a.Call(t, "pr_draft", map[string]any{"title": "fix(pricing): round negatives", "body": "Rounds them."})
	a.Emit(agent.TurnEnd{})
	a.NoneSent(t)
	p, _ := r.session(id).Primary()
	if _, err := r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: p.Name, Message: "fix(pricing): round negatives"}); err != nil {
		t.Fatal(err)
	}
	if d := r.ship(id, false)[p.Name].PRDraft; d.Stale || d.Empty() {
		t.Errorf("the PR draft after the commit: %+v", d)
	}
	turn(r, id, a)
	a.NoneSent(t)
}

// "Never overwrite what the user typed": a draft the user wrote is theirs. The agent isn't asked for it, its tool
// leaves it, and Redraft (a drop) hands it back.
func TestDraftsKeepWhatTheUserWrote(t *testing.T) {
	r, id, a, dir := draftsRig(t)
	ctx := context.Background()
	p, _ := r.session(id).Primary()
	write(t, dir, "round.go", "package pricing\n\nfunc Round(x float64) float64 { return x }\n")
	if err := r.w.ShipDraft(ctx, DraftReq{Session: id, Project: p.Name, Kind: "commit", Body: "fix: mine"}); err != nil {
		t.Fatal(err)
	}
	turn(r, id, a)
	got := a.WaitSent(t)
	if strings.Contains(got.Text, "commit_draft") || !strings.Contains(got.Text, "pr_draft") {
		t.Errorf("asked for the user's commit message: %q", got.Text)
	}
	if out := a.Call(t, "commit_draft", map[string]any{"message": "fix: theirs"}); !strings.Contains(out, "the user wrote") {
		t.Errorf("the tool said %q", out)
	}
	if d := r.ship(id, false)[p.Name].CommitDraft; d.Body != "fix: mine" || d.By != DraftByYou {
		t.Errorf("the user's draft became %+v", d)
	}

	if err := r.w.ShipDraft(ctx, DraftReq{Session: id, Project: p.Name, Kind: "commit", Drop: true}); err != nil {
		t.Fatal(err)
	}
	if out := a.Call(t, "commit_draft", map[string]any{"message": "fix: theirs"}); out != "saved" {
		t.Errorf("after a redraft the tool said %q", out)
	}
	if d := r.ship(id, false)[p.Name].CommitDraft; d.Body != "fix: theirs" || d.By != "fake" {
		t.Errorf("the redrafted draft: %+v", d)
	}
}

// A pack that doesn't draft (direct's) is never asked to.
func TestNoDraftNudgeWithoutTheTool(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("x")
	a.WaitSent(t)
	handBack(r, id, a)
	p, _ := r.session(id).Primary()
	write(t, p.Dir, "round.go", "package pricing\n")
	turn(r, id, a)
	a.NoneSent(t)
}
