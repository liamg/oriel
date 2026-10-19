package host

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

// changed starts a work session on the fake and gives its worktree a commit and uncommitted work: the change to
// review. It returns the session and its worktree.
func (r *rig) changed() (core.SessionID, *gittest.Repo) {
	r.t.Helper()
	id, a := r.workSession("Round half to even")
	a.WaitSent(r.t)
	wt := &gittest.Repo{T: r.t, Dir: r.session(id).Projects[0].Dir, When: r.repo.When}
	wt.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() int { return 2 }\n"})
	wt.Commit("Claude", "round half to even")
	wt.Write(map[string]string{"currency.go": "package pricing\n\nvar half = 1\n"})
	return id, wt
}

// reviewer is the next session a provider starts, which must be on the ai-review pack.
func reviewer(t *testing.T, f *agenttest.Fake) *agenttest.FakeSession {
	t.Helper()
	s := f.WaitStarted(t)
	if s.Spec.Pack.Name != AIReviewPack {
		t.Fatalf("%s started a session on the %q pack, want %s", f.ID(), s.Spec.Pack.Name, AIReviewPack)
	}
	return s
}

// RV-3: agents.md, Guardrails, "Cross-model review. AI review runs on a different provider from the author by
// default": the reviewer is the other provider, on the ai-review pack, given the branch's change and only read-only
// tools; whatever it asks permission for is refused; each finding it reports is a session event as it comes, and the
// review ends with its turn.
func TestAIReviewRunsOnAnotherProvider(t *testing.T) {
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4), Offers: []agent.Model{{ID: "gpt-5", Default: true}}}
	r := newRig(t, codex)
	id, wt := r.changed()
	rid, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id})
	if err != nil {
		t.Fatal(err)
	}
	rv := reviewer(t, codex)
	base := strings.TrimSpace(wt.Git("rev-parse", "--short", "main"))
	for _, want := range []string{"fake · m1 wrote it", "The change since " + base, "+func Round() int { return 2 }", "+var half = 1", "Round half to even"} {
		if !strings.Contains(rv.Spec.Prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, rv.Spec.Prompt)
		}
	}
	if rv.Spec.Model != "gpt-5" || rv.Spec.Policy != agent.PolicyAsk || rv.Spec.Cwd != r.session(id).Dir {
		t.Errorf("reviewer spec: model %q, policy %q, cwd %q", rv.Spec.Model, rv.Spec.Policy, rv.Spec.Cwd)
	}
	var tools []string
	for _, tl := range rv.Spec.Tools {
		tools = append(tools, tl.Name)
	}
	if strings.Join(tools, ",") != "finding,projects" {
		t.Errorf("the reviewer's tools: %v", tools)
	}
	se := r.session(id)
	if got, ok := se.LastAIReview(); !ok || got.ID != rid || !got.Running || got.Provider != "codex" || !got.Cross || got.Since() != base {
		t.Fatalf("the review while running: %+v", got)
	}
	denied := rv.Ask(agent.Permission{ID: "p1", Tool: "Bash", Summary: "go test ./..."})
	if d := <-denied; d.Allow || !strings.Contains(d.Note, "only reads") {
		t.Errorf("the reviewer's permission was answered %+v", d)
	}
	rv.Call(t, "finding", map[string]any{"severity": "low", "category": "style", "title": "Magic number 2", "file": "round.go", "line": 3})
	rv.Call(t, "finding", map[string]any{"severity": "high", "category": "Correctness", "title": "Historic invoices re-rounded",
		"file": "billing/invoice.go:118", "body": "Regenerate() calls RoundMinor on stored lines.", "repo": "billing-ui"})
	if out := rv.Call(t, "finding", map[string]any{"severity": "low", "title": "magic number 2", "file": "round.go"}); out != "already reported" {
		t.Errorf("a repeated finding: %q", out)
	}
	r.eventually(id, "findings as they come", func(se core.SessionState) bool { return len(se.Findings) == 2 })
	rv.Emit(agent.Message{Role: core.ItemAssistant, Text: "Two findings.\n\nOne real bug; fix it before merging."})
	rv.Emit(agent.TurnEnd{})
	se = r.eventually(id, "the review to end", func(se core.SessionState) bool { l, _ := se.LastAIReview(); return !l.Running })
	got, _ := se.LastAIReview()
	if got.Error != "" || got.Verdict != "One real bug; fix it before merging." || got.Ended.IsZero() {
		t.Errorf("the review: %+v", got)
	}
	fs := se.SortedFindings()
	if fs[0].Severity != "high" || fs[0].Category != "correctness" || fs[0].File != "billing/invoice.go" || fs[0].Line != 118 ||
		fs[0].Repo != "billing-ui" || fs[0].Review != rid || fs[0].Project != se.Projects[0].Name {
		t.Errorf("the high finding: %+v", fs[0])
	}
	if !rv.Closed() {
		t.Error("the reviewer is still running")
	}
	if ok, label := se.AIReviewGate(); ok || label != "1 high or critical open" {
		t.Errorf("gate: %v %q", ok, label)
	}
}

// RV-3: with only the author's provider installed, the review falls back to it on a different model; with no other
// model, on the same one, and says so.
func TestAIReviewFallsBackToAnotherModel(t *testing.T) {
	r := newRig(t)
	r.fake.Offers = []agent.Model{{ID: "m1", Default: true}, {ID: "m2"}}
	id, _ := r.changed()
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	rv := reviewer(t, r.fake)
	if rv.Spec.Model != "m2" {
		t.Errorf("the reviewer runs on %q, want m2", rv.Spec.Model)
	}
	rv.Emit(agent.TurnEnd{})
	se := r.eventually(id, "the review to end", func(se core.SessionState) bool { l, _ := se.LastAIReview(); return !l.Running })
	if l, _ := se.LastAIReview(); l.Provider != "fake" || l.Model != "m2" || !l.Cross {
		t.Errorf("the review: %+v", l)
	}
	r.fake.Offers = nil
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id, Full: true}); err != nil {
		t.Fatal(err)
	}
	rv = reviewer(t, r.fake)
	rv.Emit(agent.TurnEnd{})
	se = r.eventually(id, "the second review to end", func(se core.SessionState) bool {
		l, _ := se.LastAIReview()
		return len(se.AIReviews) == 2 && !l.Running
	})
	if l, _ := se.LastAIReview(); l.Model != "m1" || l.Cross {
		t.Errorf("with one model: %+v", l)
	}
}

// RV-3: "Re-review changes" reviews what changed since the last review's commit, telling the reviewer what it found
// before and what became of it; with nothing new it says so.
func TestAIReReviewIsSinceTheLastReview(t *testing.T) {
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4)}
	r := newRig(t, codex)
	id, wt := r.changed()
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	rv := reviewer(t, codex)
	rv.Call(t, "finding", map[string]any{"severity": "medium", "title": "No test for ties", "file": "round.go", "line": 3})
	rv.Emit(agent.TurnEnd{})
	se := r.eventually(id, "the review to end", func(se core.SessionState) bool { l, _ := se.LastAIReview(); return !l.Running })
	first, _ := se.LastAIReview()
	head := strings.TrimSpace(wt.Git("rev-parse", "--short", "HEAD"))
	if first.Projects[0].Head != head {
		t.Fatalf("the review's head %q, want %q", first.Projects[0].Head, head)
	}
	if err := r.w.SetFindings(core.FindingsRecorded{Session: id, Findings: []core.Finding{{ID: se.Findings[0].ID, Dismissed: true}}}); err != nil {
		t.Fatal(err)
	}
	wt.Write(map[string]string{"currency.go": "package pricing\n\nvar half = 5\n"})
	wt.Commit("Claude", "currency")
	wt.Write(map[string]string{"tiers.go": "package pricing\n"})
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	rv = reviewer(t, codex)
	if !strings.Contains(rv.Spec.Prompt, "What changed since the last review (at "+head+")") || strings.Contains(rv.Spec.Prompt, "func Round() int") ||
		!strings.Contains(rv.Spec.Prompt, "+var half = 5") || !strings.Contains(rv.Spec.Prompt, "[medium] No test for ties (round.go:3): dismissed by the user") {
		t.Errorf("the re-review's prompt:\n%s", rv.Spec.Prompt)
	}
	rv.Emit(agent.TurnEnd{})
	r.eventually(id, "the re-review to end", func(se core.SessionState) bool {
		l, _ := se.LastAIReview()
		return len(se.AIReviews) == 2 && !l.Running
	})
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err == nil || !strings.Contains(err.Error(), "nothing has changed") {
		t.Errorf("a re-review with nothing new: %v", err)
	}
}

// RV-3: a reviewer that writes its findings as JSON instead of calling the tool still has them read; a reviewer that
// fails says why.
func TestAIReviewReadsFindingsFromItsAnswer(t *testing.T) {
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4)}
	r := newRig(t, codex)
	id, _ := r.changed()
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	rv := reviewer(t, codex)
	rv.Emit(agent.Message{Role: core.ItemAssistant, Text: "Found one.\n\n```json\n{\"findings\": [{\"severity\": \"med\", \"category\": \"tests\", " +
		"\"title\": \"No test for BHD ties\", \"file\": \"round_test.go\", \"line\": \"31\", \"description\": \"The table covers JPY and USD only.\"}]}\n```\n"})
	rv.Emit(agent.TurnEnd{})
	se := r.eventually(id, "the finding", func(se core.SessionState) bool { return len(se.Findings) == 1 })
	if f := se.Findings[0]; f.Severity != "medium" || f.Line != 31 || f.Body != "The table covers JPY and USD only." {
		t.Errorf("finding: %+v", f)
	}
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id, Full: true}); err != nil {
		t.Fatal(err)
	}
	rv = reviewer(t, codex)
	rv.Emit(agent.Failure{Err: "the model is overloaded"})
	rv.Emit(agent.TurnEnd{})
	se = r.eventually(id, "the failed review", func(se core.SessionState) bool {
		l, _ := se.LastAIReview()
		return len(se.AIReviews) == 2 && !l.Running
	})
	if l, _ := se.LastAIReview(); l.Error != "the model is overloaded" {
		t.Errorf("the failed review: %+v", l)
	}
}

func TestParseFindings(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []core.Finding
	}{
		{`[{"severity":"blocker","title":"Data loss","file":"a.go:12-14"}]`, []core.Finding{{Severity: "critical", Title: "Data loss", File: "a.go", Line: 12}}},
		{"Verdict.\n```\n{\"findings\":[{\"severity\":\"nit\",\"title\":\"Name\",\"path\":\"./b.go\",\"line\":4}]}\n```",
			[]core.Finding{{Severity: "nit", Title: "Name", File: "b.go", Line: 4}}},
		{"```json\n{\"findings\": []}\n```", nil},
		{"No JSON here.", nil},
	} {
		got := ParseFindings(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("%q: %+v", tc.text, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: %+v, want %+v", tc.text, got[i], tc.want[i])
			}
		}
	}
}

// RV-3: "→ Thread" and "Fix" turn a finding into a thread on its line for the author agent, rated as the finding,
// and the finding remembers its thread; dismissing one keeps it, set aside.
func TestAFindingBecomesAThread(t *testing.T) {
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4)}
	r := newRig(t, codex)
	id, _ := r.changed()
	if _, err := r.w.AIReview(context.Background(), AIReviewReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	rv := reviewer(t, codex)
	rv.Call(t, "finding", map[string]any{"severity": "high", "title": "Ties round away from zero", "file": "round.go", "line": 3})
	rv.Emit(agent.TurnEnd{})
	se := r.eventually(id, "the finding", func(se core.SessionState) bool { return len(se.Findings) == 1 })
	f := se.Findings[0]
	tid, err := r.w.Comment(context.Background(), CommentReq{Session: id, Anchor: &core.Anchor{Project: f.Project, File: f.File, Line: f.Line},
		Title: f.Title, Body: "Fix this", Severity: f.Severity, Finding: f.ID})
	if err != nil {
		t.Fatal(err)
	}
	se = r.session(id)
	th, _ := se.Thread(tid)
	if th.Origin != core.OriginAIReview || th.Comments[0].Severity != "high" || th.Anchor.Line != 3 {
		t.Errorf("thread: %+v", th)
	}
	if got, _ := se.Finding(f.ID); got.Thread != tid {
		t.Errorf("the finding's thread: %+v", got)
	}
	if !se.FindingOpen(se.Findings[0]) {
		t.Error("a finding whose thread is open isn't open")
	}
	r.w.SetThread(ThreadReq{Session: id, Thread: tid, Status: core.ThreadResolved})
	if se := r.session(id); se.FindingOpen(se.Findings[0]) {
		t.Error("a finding whose thread is resolved is still open")
	}
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "x", Finding: "nope"}); err == nil {
		t.Error("a thread from a finding that isn't there")
	}
}

// RV-3: crit-reference.md, "review-PR with automatic AI review": a review session's change is reviewed as it starts.
func TestReviewSessionsReviewThemselves(t *testing.T) {
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4)}
	r := newRig(t, codex)
	ctx := context.Background()
	origin := r.repo
	origin.Git("checkout", "-q", "-b", "feature")
	origin.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() { /* pr */ }\n"})
	origin.Commit("Bob", "the PR")
	origin.Git("update-ref", "refs/pull/7/head", strings.TrimSpace(origin.Git("rev-parse", "HEAD")))
	origin.Git("checkout", "-q", "main")
	clone := &gittest.Repo{T: t, Dir: t.TempDir(), When: origin.When}
	clone.Git("clone", "-q", origin.Dir, ".")
	if _, err := r.w.addRepo(ctx, clone.Dir, core.RepoAdded); err != nil {
		t.Fatal(err)
	}
	id := r.start(core.SessionSpec{Kind: core.KindReview, PR: "https://github.com/acme/pricing/pull/7", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(clone.Dir)}})
	rv := reviewer(t, codex)
	if !strings.Contains(rv.Spec.Prompt, "Review this pull request (https://github.com/acme/pricing/pull/7)") ||
		!strings.Contains(rv.Spec.Prompt, "/* pr */") {
		t.Errorf("the automatic review's prompt:\n%s", rv.Spec.Prompt)
	}
	if l, ok := r.session(id).LastAIReview(); !ok || !l.Running {
		t.Errorf("no review is running: %+v", l)
	}
}
