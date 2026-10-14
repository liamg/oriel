package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// fakeQuiz is a session's fork as the quiz asks it: four questions, and grades by what the answer says.
type fakeQuiz struct {
	mu      sync.Mutex
	prompts []registry.ForkRequest
}

var quizAnswer = regexp.MustCompile(`Their answer: (.*)`)

func (f *fakeQuiz) fork(_ context.Context, r registry.ForkRequest) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, r)
	f.mu.Unlock()
	if strings.Contains(r.Prompt, "short questions about the diff") {
		return "Here they are:\n```json\n" + `{"questions": [
  {"question": "What happens to a ` + "`-2.5`" + ` tie now, and which line decides it?", "file": "round.go", "line": "42"},
  {"question": "If someone regenerates an invoice from March, can its total change?"},
  {"question": "An invoice in ` + "`XYZ`" + ` (not in the precision table) — what does the customer see?", "file": "currency.go"},
  {"question": "Why half-to-even rather than half-up?"}
]}` + "\n```", nil
	}
	m := quizAnswer.FindStringSubmatch(r.Prompt)
	if m == nil {
		return "", fmt.Errorf("not a quiz prompt: %s", r.Prompt)
	}
	switch a := m[1]; {
	case strings.Contains(a, "skipped"):
		return `{"verdict": "partly", "note": "Partly. They're skipped *because* migration 0042 marks them ` + "`Legacy`" + `. Without the migration they'd be re-rounded.", "file": "migrations/0042.sql", "line": 3}`, nil
	case strings.Contains(a, "fails"):
		return "✕ Wrong: it falls back to two places, see currency.go:12.", nil
	}
	return `{"verdict": "right", "note": "Right, including the line."}`, nil
}

func (f *fakeQuiz) asked() []registry.ForkRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.prompts)
}

func (s *errScene) verify() *verifyExt {
	for _, e := range s.exts {
		if x, ok := e.(*verifyExt); ok {
			return x
		}
	}
	return nil
}

// verifySettle frames until the fork has answered and what it said is drawn.
func (s *errScene) verifySettle(t *testing.T, what string, ok func(frame string) bool) {
	t.Helper()
	s.until(t, what, func() bool {
		s.verify().wait()
		s.ship().wait()
		s.sh.runTodo()
		s.frame()
		return ok(s.frameText())
	})
}

// verifyScene is 18's session with a commit on usage-api to raise, and a fake fork in front of the agent's.
func verifyScene(t *testing.T) (*errScene, *fakeQuiz, core.SessionState, func() []gh.PR) {
	s, _, hub, se, _ := shipScene(t)
	s.reg.Options().Unset(verifyOption, registry.LayerUser, registry.At{})
	x := s.ship()
	writeFiles(t, shipDir(se, "usage-api"), map[string]string{
		"round.go":            "package usage\n\nfunc round() {}\n",
		"migrations/0042.sql": "-- 0042\nALTER TABLE invoice_lines\n  ADD COLUMN legacy boolean NOT NULL DEFAULT true;\n",
	})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the files", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return len(sh.Files) == 2 })
	s.run("ship.commit", registry.Args{"project": "usage-api", "message": "fix(pricing): round invoice lines half-to-even"})
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.Commits == 1 })
	f := &fakeQuiz{}
	s.reg.Contribute(registry.Forker{Name: "fake", Fork: f.fork, Label: func(string) string { return "claude · fork, no tools" }})
	return s, f, se, func() []gh.PR { return hub.PRs("acme/usage-api") }
}

// answer types into the quiz's composer and sends it with ⌘↵.
func (s *errScene) answer(t *testing.T, text string) {
	t.Helper()
	s.typeInto(t, "Your answer", text)
	s.keyCommand(t, "<D-CR>")
}

// RV-7, ui.md 13: "The quiz before the PR, as a chat. Each answer gets a verdict, and a partial one points at the
// file to read." agents.md: "the agent asks you about the change before the PR goes out", through the fork
// ("Side questions (btw, explain, verify quiz): $.model.fork: tool-less, outside the main conversation").
func TestVerifyQuizBeforeRaise(t *testing.T) {
	s, f, se, prs := verifyScene(t)
	x := s.verify()

	// The agent's forker answers side questions for the session, through its worker.
	if fs := registry.Forkers(s.reg); len(fs) != 2 || fs[0].Name != "fake" || fs[1].Name != "agent" {
		t.Fatalf("forkers: %+v", fs)
	}
	if got := registry.ForkLabel(s.reg, string(se.ID)); got != "claude · fork, no tools" {
		t.Errorf("the label: %q", got)
	}

	s.run("ship.pr.open", registry.Args{"project": "usage-api"})
	s.until(t, "the tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	gates := func() map[string]registry.ShipGateState {
		out := map[string]registry.ShipGateState{}
		for _, g := range registry.ShipGates(s.reg, string(se.ID), string(se.Kind)) {
			out[g.Name] = g
		}
		return out
	}
	if g := gates()["verify"]; g.Title != "Verify quiz" || g.State != registry.GatePending || g.Note != "before raise" {
		t.Errorf("the gate before the quiz: %+v", g)
	}

	// Raise: the quiz stops it and opens over the tab.
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.verifySettle(t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	if len(prs()) != 0 {
		t.Fatal("the PR was raised before the quiz")
	}
	s.wantFrame(t, "VERIFY BEFORE RAISING", "claude · fork, no tools", "Can you explain this change?",
		"Four short questions about the diff", "if you want them there.",
		"0/4", "What happens to a", "-2.5", "tie now, and which line decides it?", "⌘↵ send", "open the file it's asking about",
		"Skip verification (logged)", "verify-before-PR: on for work sessions")
	if asked := f.asked(); len(asked) != 1 || asked[0].Session != string(se.ID) || !strings.Contains(asked[0].Prompt, "usage-api") {
		t.Errorf("the questions were asked: %+v", asked)
	}
	s.lint("the verify quiz")

	// Question 1: right.
	s.answer(t, "It rounds to -2: the tie check at round.go:42 returns the even one.")
	s.verifySettle(t, "the first verdict", func(f string) bool { return strings.Contains(f, "QUESTION 2") })
	s.wantFrame(t, "It rounds to -2", "✓", "Right, including the line.", "1/4", "If someone regenerates an invoice from March")
	if g := gates()["verify"]; g.State != registry.GatePending || g.Note != "1/4" {
		t.Errorf("the gate during the quiz: %+v", g)
	}

	// Question 2: partly, pointing at the migration; ⌥O opens it, and the quiz waits.
	s.answer(t, "No, the old lines are skipped.")
	s.verifySettle(t, "the partial verdict", func(f string) bool { return strings.Contains(f, "QUESTION 3") })
	s.wantFrame(t, "Partly. They're skipped", "Legacy", "Worth a look:", "0042.sql:3", "2/4")
	s.keyCommand(t, "<M-o>")
	s.until(t, "the migration open", func() bool {
		return slices.ContainsFunc(s.visibleRefs(), func(v layout.ViewRef) bool {
			f, _ := s.editorFile(v.Instance)
			return v.ID == EditorView && f == filepath.Join(shipDir(se, "usage-api"), "migrations", "0042.sql")
		})
	})
	if x.open != "" {
		t.Error("the quiz stayed open over the file")
	}

	// Raising again goes back to the quiz where it was.
	s.run("ship.pr.open", registry.Args{"project": "usage-api"})
	s.until(t, "the tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.verifySettle(t, "the quiz again", func(f string) bool { return strings.Contains(f, "VERIFY BEFORE RAISING") })
	s.wantFrame(t, "QUESTION 3", "XYZ", "2/4")
	if len(prs()) != 0 {
		t.Fatal("the PR was raised mid-quiz")
	}
	s.typeInto(t, "Your answer", "It shows two decimal places, the default.")
	s.click(centre(s.access("Send").Bounds))
	s.verifySettle(t, "question 4", func(f string) bool { return strings.Contains(f, "QUESTION 4") })
	s.answer(t, "Half-to-even doesn't drift on sums of many lines.")
	s.verifySettle(t, "the outcome", func(f string) bool { return strings.Contains(f, "Verified") })
	s.wantFrame(t, "Verified · 3 right · 1 partly", "Add the answers to the PR as reviewer notes", "Raise PR", "4/4")
	if g := gates()["verify"]; g.State != registry.GatePass || g.Note != "3/4 right" {
		t.Errorf("the gate after the quiz: %+v", g)
	}

	// Raise PR goes on with the raise, the answers in its body as reviewer notes.
	s.click(centre(s.access("Raise PR").Bounds))
	s.verifySettle(t, "the PR", func(string) bool { return len(prs()) == 1 })
	body := prs()[0].Body
	for _, want := range []string{"<!-- oriel:verify -->", "### Reviewer notes", "Verified before raising (claude · fork, no tools): 3 of 4 right, 1 partly.",
		"**If someone regenerates an invoice from March, can its total change?**\nNo, the old lines are skipped.\n◐ Partly.", "<!-- /oriel:verify -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("the PR's body has no %q:\n%s", want, body)
		}
	}
	got := waitState(t, s.store, "the verification recorded", func(st core.State) bool {
		se, _ := st.Session(se.ID)
		_, ok := se.Snapshot().Verification()
		return ok
	})
	sess, _ := got.Session(se.ID)
	v, _ := sess.Snapshot().Verification()
	if v.Result != core.VerifyPassed || len(v.Questions) != 4 || v.Questions[1].Verdict != core.VerdictPartly ||
		v.Questions[1].File != "migrations/0042.sql" || v.By != "claude · fork, no tools" {
		t.Errorf("the verification: %+v", v)
	}
	if x.open != "" {
		t.Error("the quiz is still open after the raise")
	}
}

// RV-7: the reviewer notes go in the PR when chosen, and not when not.
func TestVerifyNotesInThePR(t *testing.T) {
	for _, notes := range []bool{true, false} {
		t.Run(fmt.Sprint(notes), func(t *testing.T) {
			s, _, se, prs := verifyScene(t)
			if err := s.host.Run(context.Background(), "ship.raise", registry.Args{"project": "usage-api"}); err == nil {
				t.Fatal("the raise went through before the quiz")
			}
			s.verifySettle(t, "the quiz", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
			for i := range 4 {
				s.answer(t, "the tie check at round.go:42")
				s.verifySettle(t, "a verdict", func(string) bool { return s.verify().quizzes[se.ID].at == i+1 })
			}
			if !notes {
				s.click(centre(s.access("Add the answers to the PR as reviewer notes").Bounds))
			}
			s.click(centre(s.access("Raise PR").Bounds))
			s.verifySettle(t, "the PR", func(string) bool { return len(prs()) == 1 })
			if got := strings.Contains(prs()[0].Body, "### Reviewer notes"); got != notes {
				t.Errorf("reviewer notes in the PR: %v, want %v:\n%s", got, notes, prs()[0].Body)
			}
		})
	}
}

// RV-7, ui.md 13: "A skip is allowed, and logged."
func TestVerifySkipIsLogged(t *testing.T) {
	s, _, se, prs := verifyScene(t)
	s.run("ship.pr.open", registry.Args{"project": "usage-api"})
	s.until(t, "the tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.verifySettle(t, "the quiz", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	s.click(centre(s.access("Skip verification (logged)").Bounds))
	s.verifySettle(t, "the PR", func(string) bool { return len(prs()) == 1 })
	waitState(t, s.store, "the skip recorded", func(st core.State) bool {
		se, _ := st.Session(se.ID)
		v, ok := se.Snapshot().Verification()
		return ok && v.Result == core.VerifySkipped
	})
	if !slices.ContainsFunc(s.sh.cfg.Logs.Ring.Records(), func(r logs.Record) bool {
		return r.Session == se.ID && r.Level == slog.LevelWarn && r.Msg == "verification skipped"
	}) {
		t.Error("the skip isn't in the session's log")
	}
	if strings.Contains(prs()[0].Body, "Reviewer notes") {
		t.Error("a skipped quiz added reviewer notes")
	}
	if g := registry.ShipGates(s.reg, string(se.ID), string(se.Kind)); !slices.ContainsFunc(g, func(g registry.ShipGateState) bool {
		return g.Name == "verify" && g.State == registry.GateWarn && g.Note == "skipped"
	}) {
		t.Errorf("the gate after a skip: %+v", g)
	}
}

// RV-7: verify-before-PR is per repo (.oriel/settings.toml); off, a raise goes straight through.
func TestVerifyOffForARepo(t *testing.T) {
	s, f, se, prs := verifyScene(t)
	var repo string
	for _, p := range se.Projects {
		if p.Name == "usage-api" {
			repo = cmpOr(p.Repo, p.Dir)
		}
	}
	if err := s.reg.Options().Set(verifyOption, registry.LayerProject, registry.At{Project: repo}, "off"); err != nil {
		t.Fatal(err)
	}
	if g := registry.ShipGates(s.reg, string(se.ID), string(se.Kind)); !slices.ContainsFunc(g, func(g registry.ShipGateState) bool {
		return g.Name == "verify" && g.State == registry.GateOptional && g.Note == "off for this repo"
	}) {
		t.Errorf("the gate with verify off: %+v", g)
	}
	s.run("ship.raise", registry.Args{"project": "usage-api"})
	s.verifySettle(t, "the PR", func(string) bool { return len(prs()) == 1 })
	if s.verify().open != "" || len(f.asked()) != 0 {
		t.Error("the quiz ran for a repo with it off")
	}
}

// RV-7: the fork's replies are read robustly: JSON as asked (in a fence, with a line as a string), else a list or
// questions, and a grade from its first word or glyph, pointing at the file it names.
func TestQuizReplies(t *testing.T) {
	qs := parseQuestions("Sure.\n```json\n{\"questions\": [{\"q\": \"Which line decides a tie?\", \"file\": \"round.go\", \"line\": \"42\"}, {\"question\": \"What does `XYZ` show?\"}]}\n```")
	if len(qs) != 2 || qs[0].Question != "Which line decides a tie?" || qs[0].File != "round.go" || qs[0].Line != 42 || qs[1].Question != "What does `XYZ` show?" {
		t.Errorf("JSON questions: %+v", qs)
	}
	qs = parseQuestions("1. What does round.go:42 return for -2.5?\n2) Can March's total change?\n- Why half-to-even?\n- one\n- two\nNot a question.")
	if len(qs) != quizLength || qs[0].File != "round.go" || qs[0].Line != 42 || qs[1].Question != "Can March's total change?" || qs[2].Question != "Why half-to-even?" {
		t.Errorf("listed questions: %+v", qs)
	}
	if qs := parseQuestions("What changed?\nAnd why?"); len(qs) != 2 {
		t.Errorf("bare questions: %+v", qs)
	}
	for _, tt := range []struct {
		reply string
		want  verdict
	}{
		{`{"verdict": "Right", "note": "Right, including the line."}`, verdict{Verdict: core.VerdictRight, Note: "Right, including the line."}},
		{`{"verdict": "partly", "note": "Worth a look at 0042.sql before raising."}`, verdict{Verdict: core.VerdictPartly, Note: "Worth a look at 0042.sql before raising.", File: "0042.sql"}},
		{`{"verdict": "wrong", "note": "No.", "file": "currency.go", "line": 12}`, verdict{Verdict: core.VerdictWrong, Note: "No.", File: "currency.go", Line: 12}},
		{"✕ Wrong: see currency.go:12.", verdict{Verdict: core.VerdictWrong, Note: "Wrong: see currency.go:12.", File: "currency.go", Line: 12}},
		{"Correct.", verdict{Verdict: core.VerdictRight, Note: "Correct."}},
		{"Nothing to add, -2.5 rounds to -2.", verdict{Verdict: core.VerdictPartly, Note: "Nothing to add, -2.5 rounds to -2."}},
	} {
		if got := parseVerdict(tt.reply); got != tt.want {
			t.Errorf("%q: %+v, want %+v", tt.reply, got, tt.want)
		}
	}
}
