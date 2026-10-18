package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// reviewSession is a work session on the fake whose worktree changes round.go from a committed base, with a second
// provider, codex, installed to review it; the diff of the change is open.
func reviewSession(t *testing.T) (*running, *agenttest.FakeSession, *agenttest.Fake, *localHost) {
	t.Helper()
	author := agenttest.NewFake()
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4), Offers: []agent.Model{{ID: "gpt-5", Default: true}}}
	lw := newLocalHost(t, author, codex)
	lw.fake = author
	r, a, _ := agentSessionOn(t, lw)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(roundBase), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(roundAgent), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.waitFrame("the diff with the AI review column", func(s string) bool {
		return containsAll(s, "func roundHalfEven", "AI REVIEW", "No AI review yet.", "REVIEWER", "Review changes", "Prompt…")
	})
	return r, a, codex, lw
}

// waitReviewer is the reviewer codex starts.
func waitReviewer(t *testing.T, codex *agenttest.Fake) *agenttest.FakeSession {
	t.Helper()
	s := codex.WaitStarted(t)
	if s.Spec.Pack.Name != "ai-review" {
		t.Fatalf("codex started on the %q pack", s.Spec.Pack.Name)
	}
	return s
}

// above says whether the first run starting a is drawn above the first starting b.
func above(f headless.Frame, a, b string) bool {
	y := func(s string) (float32, bool) {
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && strings.HasPrefix(op.Run.Text, s) {
				return op.At.Y, true
			}
		}
		return 0, false
	}
	ya, oka := y(a)
	yb, okb := y(b)
	return oka && okb && ya < yb
}

// RV-3: ui.md 06, "Cross-model AI review: Codex reviews Claude's work (or the other way round), with findings sorted
// by severity", as the mockup's right column: Review changes runs it on the other provider; the header names the
// reviewer and the commit, chips count each severity, the cards sit worst first with their kicker, title, place
// and body, and a finding about a repo the session hasn't got offers Add project. The foot says the reviewer is a
// different model from the author.
func TestAIReviewColumn(t *testing.T) {
	r, _, codex, _ := reviewSession(t)
	base := strings.TrimSpace(git(t, projectDir(t, r), "rev-parse", "--short", "HEAD"))
	r.clickText("Review changes")
	rv := waitReviewer(t, codex)
	if !strings.Contains(rv.Spec.Prompt, "+\tt := scaled.Truncate(0)") {
		t.Errorf("the reviewer wasn't given the change:\n%s", rv.Spec.Prompt)
	}
	r.waitText("Codex is reviewing the change…")
	for _, f := range []map[string]any{
		{"severity": "low", "category": "style", "title": "Magic number 2 for default precision", "file": "round.go", "line": 80},
		{"severity": "medium", "category": "tests", "title": "No test for BHD (3 dp) ties", "file": "round_test.go", "line": 31,
			"body": "The table covers JPY and USD only."},
		{"severity": "high", "category": "correctness", "title": "Historic invoices re-rounded on regenerate", "file": "billing/invoice.go", "line": 118,
			"body": "`Regenerate()` calls `RoundMinor` on stored lines, so PDFs of old invoices would change."},
		{"severity": "medium", "category": "api", "title": "Exported signature change", "file": "round.go", "line": 78,
			"body": "billing-ui calls `RoundMinor`; that repo isn't in this workspace.", "repo": "billing-ui"},
	} {
		rv.Call(t, "finding", f)
	}
	rv.Emit(agent.TurnEnd{})
	f := r.latest("the findings", func(f headless.Frame) bool {
		return containsAll(f.Text(), "CODEX · GPT-5", "since "+base, "1 HIGH", "2 MED", "1 LOW", "HIGH", "CORRECTNESS",
			"Historic invoices re-rounded", "regenerate", "billing/invoice.go:118", "Regenerate()", "No test for BHD (3 dp) ties", "round_test.go:31",
			"Exported signature change", "Add project", "→ Thread", "Fix", "Dismiss", "different model from the author", "Re-review changes")
	})
	if !above(f, "Historic invoices re-rounded", "No test for BHD (3 dp) ties") ||
		!above(f, "Exported signature change", "Magic number 2 for default precision") {
		t.Errorf("the cards aren't worst first:\n%s", f.Text())
	}
	if ok, label := sessionState(t, r).AIReviewGate(); ok || label != "1 high or critical open" {
		t.Errorf("the gate: %v %q", ok, label)
	}
}

// RV-3: the severity chips filter the cards, Dismiss sets one aside behind "n dismissed", and Restore brings it back.
func TestAIReviewFilterAndDismiss(t *testing.T) {
	r, _, codex, _ := reviewSession(t)
	r.clickText("Review changes")
	rv := waitReviewer(t, codex)
	rv.Call(t, "finding", map[string]any{"severity": "high", "title": "Historic invoices re-rounded", "file": "round.go", "line": 8})
	rv.Call(t, "finding", map[string]any{"severity": "low", "title": "Magic number 2", "file": "round.go", "line": 9})
	rv.Emit(agent.TurnEnd{})
	r.waitFrame("both findings", func(s string) bool { return containsAll(s, "Historic invoices re-rounded", "Magic number 2") })
	r.clickText("1 HIGH")
	r.waitGone("Magic number 2")
	r.waitText("Historic invoices re-rounded")
	r.clickText("1 HIGH")
	r.waitText("Magic number 2")
	r.clickAccess("Dismiss")
	r.waitFrame("one dismissed", func(s string) bool {
		return strings.Contains(s, "1 dismissed") && strings.Count(s, "Historic invoices re-rounded")+strings.Count(s, "Magic number 2") == 1
	})
	se := sessionState(t, r)
	gone := se.SortedFindings()[0]
	if !gone.Dismissed || gone.Severity != "high" {
		t.Fatalf("dismissed: %+v", se.Findings)
	}
	if ok, _ := se.AIReviewGate(); !ok {
		t.Error("a dismissed high finding still holds up the gate")
	}
	r.clickText("1 dismissed")
	r.waitFrame("the dismissed one shown", func(s string) bool { return containsAll(s, "Historic invoices re-rounded", "Restore") })
	r.clickText("Restore")
	waitState(t, r.store, "the finding back", func(st core.State) bool {
		se, _ := st.Session("w")
		return !se.Snapshot().SortedFindings()[0].Dismissed
	})
	r.waitGone("dismissed")
}

// RV-3: → Thread hands a finding to the author agent as a thread on its line, rated as the finding, and Fix asks it
// to fix one; the card then links to its thread. A finding's place selects its line in the diff.
func TestAIReviewFindingToThreadAndFix(t *testing.T) {
	r, a, codex, _ := reviewSession(t)
	r.clickText("Review changes")
	rv := waitReviewer(t, codex)
	rv.Call(t, "finding", map[string]any{"severity": "high", "category": "correctness", "title": "Negative ties round the wrong way",
		"file": "round.go", "line": 8, "body": "Truncate goes toward zero."})
	rv.Call(t, "finding", map[string]any{"severity": "medium", "category": "tests", "title": "No test for ties", "file": "round_test.go", "line": 3})
	rv.Emit(agent.TurnEnd{})
	r.waitText("round.go:8")
	r.clickText("round.go:8")
	r.waitAccess("the finding's line selected", func(r *running) bool { n := r.access("\tt := scaled.Truncate(0)"); return n != nil && n.Selected })
	r.clickText("→ Thread")
	in := a.WaitSent(t)
	for _, want := range []string{"round.go:8", "An AI review by codex · gpt-5 found (high, correctness): **Negative ties round the wrong way**",
		"Truncate goes toward zero.", "Is it right?"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the agent got no %q:\n%s", want, in.Text)
		}
	}
	st := waitState(t, r.store, "the finding's thread", func(st core.State) bool {
		se, _ := st.Session("w")
		return se.Snapshot().SortedFindings()[0].Thread != ""
	})
	sw, _ := st.Session("w")
	se := sw.Snapshot()
	f := se.SortedFindings()[0]
	th, ok := se.Thread(f.Thread)
	if !ok || th.Origin != core.OriginAIReview || th.Comments[0].Severity != "high" || th.Anchor == nil || th.Anchor.Line != 8 {
		t.Fatalf("the finding's thread: %+v (%+v)", th, f)
	}
	r.waitText("Thread · open")
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.clickText("Fix")
	in = a.WaitSent(t)
	if !strings.Contains(in.Text, "No test for ties") || !strings.Contains(in.Text, "Fix it, then reply here with what you changed.") {
		t.Errorf("the fix request:\n%s", in.Text)
	}
}

// RV-3: Add project adds the repo a finding is about to the session (session.add-repo), and the finding can then go
// to the agent.
func TestAIReviewAddProject(t *testing.T) {
	r, _, codex, lw := reviewSession(t)
	other := filepath.Join(t.TempDir(), "billing-ui")
	for _, args := range [][]string{{"init", "-q", "-b", "main", other}, {"-C", other, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := lw.w.AddRepoPath(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	r.clickText("Review changes")
	rv := waitReviewer(t, codex)
	rv.Call(t, "finding", map[string]any{"severity": "medium", "category": "api", "title": "Exported signature change", "file": "round.go",
		"line": 12, "repo": "billing-ui"})
	rv.Emit(agent.TurnEnd{})
	r.clickText("Add project")
	waitState(t, r.store, "billing-ui in the session", func(st core.State) bool {
		se, _ := st.Session("w")
		return hasRepo(se.Snapshot(), "billing-ui")
	})
	r.waitFrame("the finding can go to the agent", func(s string) bool { return strings.Contains(s, "→ Thread") && !strings.Contains(s, "Add project") })
}

// RV-3: Prompt… opens the reviewer's instructions as a file of the user's, made from the built-in pack, and the next
// review runs on what was saved; Re-review changes reads only what changed since the last review.
func TestAIReviewPromptAndReReview(t *testing.T) {
	r, _, codex, _ := reviewSession(t)
	r.clickText("Review changes")
	rv := waitReviewer(t, codex)
	rv.Emit(agent.TurnEnd{})
	r.waitFrame("the review done", func(s string) bool { return containsAll(s, "No findings.", "Re-review changes") })
	r.clickText("Prompt…")
	r.waitText("# AI review")
	var path string
	r.do(func() error { path = r.host.Here().Buffer; return nil })
	if filepath.Base(path) != "ai-review.md" || filepath.Base(filepath.Dir(path)) != "agents" {
		t.Fatalf("the editor is on %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "mcp__oriel__finding") {
		t.Fatalf("the prompt file: %v %q", err, b)
	}
	os.WriteFile(path, []byte(strings.Replace(string(b), "# AI review", "# AI review\n\nBe brief.", 1)), 0o644)
	os.WriteFile(filepath.Join(projectDir(t, r), "round.go"), []byte(roundWork), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "review.ai.run", nil) })
	rv = waitReviewer(t, codex)
	if !strings.Contains(rv.Spec.Pack.Text, "Be brief.") {
		t.Errorf("the reviewer's pack isn't the user's:\n%s", rv.Spec.Pack.Text)
	}
	if !strings.Contains(rv.Spec.Prompt, "What changed since the last review") || !strings.Contains(rv.Spec.Prompt, "currency.Precision") ||
		strings.Contains(rv.Spec.Prompt, "+\tt := scaled.Truncate(0)") {
		t.Errorf("the re-review's prompt:\n%s", rv.Spec.Prompt)
	}
}

func sessionState(t *testing.T, r *running) core.SessionState {
	t.Helper()
	se, ok := r.store.Session("w")
	if !ok {
		t.Fatal("no session w")
	}
	return se.Snapshot()
}

// aiReviewScene is the mockup's diff in a session with the mockup's AI review: four findings, two dismissed.
func aiReviewScene(t *testing.T) *errScene {
	t.Helper()
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	f := func(id, sev, cat, title, file string, line int, body, repo string) core.Finding {
		return core.Finding{ID: id, Review: "r1", Severity: sev, Category: cat, Title: title, Project: "pricing", File: file, Line: line, Body: body, Repo: repo}
	}
	gone := f("f5", "low", "style", "Comment says half-up", "internal/pricing/round.go", 5, "", "")
	gone.Dismissed = true
	gone2 := f("f6", "low", "docs", "CHANGELOG entry is vague", "CHANGELOG.md", 3, "", "")
	gone2.Dismissed = true
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing-rounding", Kind: core.KindWork,
			Projects: []core.Project{{Name: "pricing", Dir: r.Dir, Primary: true}}, Agent: core.AgentState{Provider: "claude"}}},
		core.SessionFocused{ID: "pricing-rounding"},
		core.AIReviewChanged{Session: "pricing-rounding", Review: core.AIReview{ID: "r1", Provider: "codex", Model: "gpt-5", Cross: true,
			Projects: []core.ReviewedProject{{Project: "pricing", Since: "a3f9e1"}}}},
		core.FindingsRecorded{Session: "pricing-rounding", Findings: []core.Finding{
			f("f1", "high", "correctness", "Historic invoices re-rounded on regenerate", "billing/invoice.go", 118,
				"`Regenerate()` calls `RoundMinor` on stored lines, so PDFs of old invoices would change.", ""),
			f("f2", "medium", "tests", "No test for BHD (3 dp) ties", "round_test.go", 31, "The table covers JPY and USD only.", ""),
			f("f3", "medium", "api", "Exported signature change", "round.go", 78, "billing-ui calls `RoundMinor`; that repo isn't in this workspace.", "billing-ui"),
			f("f4", "low", "style", "Magic number 2 for default precision", "round.go", 80, "", ""),
			gone, gone2,
		}},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.sh.git.wait()
	s.run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	for range 3 {
		s.frame()
		s.diff().wait()
		s.sh.runTodo()
	}
	s.frame()
	return s
}

// RV-3: every control of the AI review's column resolves to its help (help.md's lint).
func TestAIReviewColumnHelp(t *testing.T) {
	s := aiReviewScene(t)
	if txt := s.frameText(); !containsAll(txt, "CODEX · GPT-5", "since a3f9e1", "1 HIGH", "2 MED", "1 LOW", "2 dismissed", "Add project") {
		t.Fatalf("the column isn't drawn:\n%s", txt)
	}
	s.lint("the AI review")
}
