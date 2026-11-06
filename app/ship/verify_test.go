package ship_test

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
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

func verifyOf(s *apptest.Scene) *ship.Verify { return apptest.PluginOf[*ship.Verify](s, "verify") }

// verifySettle frames until the fork has answered and what it said is drawn.
func verifySettle(s *apptest.Scene, t *testing.T, what string, ok func(frame string) bool) {
	t.Helper()
	s.Until(what, func() bool {
		verifyOf(s).Wait()
		shipOf(s).Wait()
		s.RunQueued()
		s.Frame()
		return ok(s.Text())
	})
}

// verifyScene is the Ship session with a commit on search-api to raise, and a fake fork in front of the agent's.
func verifyScene(t *testing.T) (*apptest.Scene, *fakeQuiz, core.SessionState, func() []gh.PR) {
	s, _, hub, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{
		"round.go":            "package usage\n\nfunc round() {}\n",
		"migrations/0042.sql": "-- 0042\nALTER TABLE invoice_lines\n  ADD COLUMN legacy boolean NOT NULL DEFAULT true;\n",
	})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the files", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 2 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "fix(pricing): round invoice lines half-to-even"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	f := &fakeQuiz{}
	s.Registry().Contribute(registry.Forker{Name: "fake", Fork: f.fork, Label: func(string) string { return "claude · fork, no tools" }})
	return s, f, se, func() []gh.PR { return hub.PRs("acme/search-api") }
}

// answer types into the quiz's composer and sends it with ⌘↵.
func answer(s *apptest.Scene, t *testing.T, text string) {
	t.Helper()
	s.TypeInto("Your answer", text)
	s.Press("<D-CR>")
}

// The quiz before the PR, as a chat: each answer gets a verdict, and a partial one points at the file to read. It's
// optional: you start it from its row in "before this ships", and a raise never waits for it. It goes through the
// fork ($.model.fork: tool-less, outside the main conversation).
func TestVerifyQuiz(t *testing.T) {
	s, f, se, prs := verifyScene(t)
	x := verifyOf(s)

	// The agent's forker answers side questions for the session, through the session host.
	if fs := registry.Forkers(s.Registry()); len(fs) != 2 || fs[0].Name != "fake" || fs[1].Name != "agent" {
		t.Fatalf("forkers: %+v", fs)
	}
	if got := registry.ForkLabel(s.Registry(), string(se.ID)); got != "claude · fork, no tools" {
		t.Errorf("the label: %q", got)
	}

	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	gates := func() map[string]registry.ShipGateState {
		out := map[string]registry.ShipGateState{}
		for _, g := range registry.ShipGates(s.Registry(), string(se.ID), string(se.Kind)) {
			out[g.Name] = g
		}
		return out
	}
	if g := gates()["verify"]; g.Title != "Verify quiz" || g.State != registry.GateOptional || g.Note != "optional" || g.Action != "Verify…" {
		t.Errorf("the gate before the quiz: %+v", g)
	}

	// Verify… on its row opens the quiz over the tab.
	verifySettle(s, t, "the gates counted", func(f string) bool { return !strings.Contains(f, "counting") })
	s.ClickOn("Verify…")
	verifySettle(s, t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	s.WantFrame("VERIFY THIS CHANGE", "claude · fork, no tools", "Can you explain this change?",
		"Four short questions about the diff", "if you want them there.",
		"0/4", "What happens to a", "-2.5", "tie now, and which line decides it?", "⌘↵ send", "open the file it's asking about",
		"optional: a raise never waits for it")
	if asked := f.asked(); len(asked) != 1 || asked[0].Session != string(se.ID) || !strings.Contains(asked[0].Prompt, "search-api") {
		t.Errorf("the questions were asked: %+v", asked)
	}
	s.Lint("the verify quiz")

	// Question 1: right.
	answer(s, t, "It rounds to -2: the tie check at round.go:42 returns the even one.")
	verifySettle(s, t, "the first verdict", func(f string) bool { return strings.Contains(f, "QUESTION 2") })
	s.WantFrame("It rounds to -2", "✓", "Right, including the line.", "1/4", "If someone regenerates an invoice from March")
	if g := gates()["verify"]; g.State != registry.GatePending || g.Note != "1/4" || g.Action != "Resume" {
		t.Errorf("the gate during the quiz: %+v", g)
	}

	// Question 2: partly, pointing at the migration; ⌥O opens it, and the quiz waits.
	answer(s, t, "No, the old lines are skipped.")
	verifySettle(s, t, "the partial verdict", func(f string) bool { return strings.Contains(f, "QUESTION 3") })
	s.WantFrame("Partly. They're skipped", "Legacy", "Worth a look:", "0042.sql:3", "2/4")
	s.Press("<M-o>")
	s.Until("the migration open", func() bool {
		return slices.ContainsFunc(s.Host().Layout().Visible(), func(v layout.ViewRef) bool {
			f, _ := editorFile(s, v.Instance)
			return v.ID == kit.EditorView && f == filepath.Join(shipDir(se, "search-api"), "migrations", "0042.sql")
		})
	})
	if x.Open() != "" {
		t.Error("the quiz stayed open over the file")
	}

	// Resume on its row goes back to the quiz where it was.
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.ClickOn("Resume")
	verifySettle(s, t, "the quiz again", func(f string) bool { return strings.Contains(f, "VERIFY THIS CHANGE") })
	s.WantFrame("QUESTION 3", "XYZ", "2/4")
	s.TypeInto("Your answer", "It shows two decimal places, the default.")
	s.ClickOn("Send")
	verifySettle(s, t, "question 4", func(f string) bool { return strings.Contains(f, "QUESTION 4") })
	answer(s, t, "Half-to-even doesn't drift on sums of many lines.")
	verifySettle(s, t, "the outcome", func(f string) bool { return strings.Contains(f, "Verified") })
	s.WantFrame("Verified · 3 right · 1 partly", "Add the answers to the PR as reviewer notes", "Done", "4/4")
	if g := gates()["verify"]; g.State != registry.GatePass || g.Note != "3/4 right" {
		t.Errorf("the gate after the quiz: %+v", g)
	}
	if len(prs()) != 0 {
		t.Fatal("the quiz raised the PR")
	}

	// Done closes it; the raise that follows carries the answers as reviewer notes.
	s.ClickOn("Done")
	s.Frame()
	if x.Open() != "" {
		t.Error("Done left the quiz open")
	}
	s.ClickOn("Push + raise PR")
	verifySettle(s, t, "the PR", func(string) bool { return len(prs()) == 1 })
	body := prs()[0].Body
	for _, want := range []string{"<!-- oriel:verify -->", "### Reviewer notes", "Verified before raising (claude · fork, no tools): 3 of 4 right, 1 partly.",
		"**If someone regenerates an invoice from March, can its total change?**\nNo, the old lines are skipped.\n◐ Partly.", "<!-- /oriel:verify -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("the PR's body has no %q:\n%s", want, body)
		}
	}
	got := s.UntilState("the verification recorded", func(st core.State) bool {
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
}

// The reviewer notes go in the PR when chosen, and not when not.
func TestVerifyNotesInThePR(t *testing.T) {
	for _, notes := range []bool{true, false} {
		t.Run(fmt.Sprint(notes), func(t *testing.T) {
			s, _, se, prs := verifyScene(t)
			s.Run("verify.open", nil)
			verifySettle(s, t, "the quiz", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
			for i := range 4 {
				answer(s, t, "the tie check at round.go:42")
				verifySettle(s, t, "a verdict", func(string) bool { return verifyOf(s).Answered(se.ID) == i+1 })
			}
			if !notes {
				s.ClickOn("Add the answers to the PR as reviewer notes")
			}
			s.ClickOn("Done")
			s.Run("ship.raise", registry.Args{"project": "search-api"})
			verifySettle(s, t, "the PR", func(string) bool { return len(prs()) == 1 })
			if got := strings.Contains(prs()[0].Body, "### Reviewer notes"); got != notes {
				t.Errorf("reviewer notes in the PR: %v, want %v:\n%s", got, notes, prs()[0].Body)
			}
		})
	}
}

// Verify is a thing of its own; a raise never waits for it, taken or not, nor one left part way.
func TestRaiseNeverWaitsForVerify(t *testing.T) {
	s, f, se, prs := verifyScene(t)
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.ClickOn("Push + raise PR")
	verifySettle(s, t, "the PR", func(string) bool { return len(prs()) == 1 })
	if verifyOf(s).Open() != "" || len(f.asked()) != 0 {
		t.Error("raising started the quiz")
	}
	if strings.Contains(prs()[0].Body, "Reviewer notes") {
		t.Error("a PR raised without the quiz has reviewer notes")
	}
	if g := registry.ShipGates(s.Registry(), string(se.ID), string(se.Kind)); !slices.ContainsFunc(g, func(g registry.ShipGateState) bool {
		return g.Name == "verify" && g.State == registry.GateOptional
	}) {
		t.Errorf("the gate after a raise without it: %+v", g)
	}
}

// The fork's replies are read robustly: JSON as asked (in a fence, with a line as a string), else a list or
// questions, and a grade from its first word or glyph, pointing at the file it names.
func TestQuizReplies(t *testing.T) {
	qs := ship.ParseQuestions("Sure.\n```json\n{\"questions\": [{\"q\": \"Which line decides a tie?\", \"file\": \"round.go\", \"line\": \"42\"}, {\"question\": \"What does `XYZ` show?\"}]}\n```")
	if len(qs) != 2 || qs[0].Question != "Which line decides a tie?" || qs[0].File != "round.go" || qs[0].Line != 42 || qs[1].Question != "What does `XYZ` show?" {
		t.Errorf("JSON questions: %+v", qs)
	}
	qs = ship.ParseQuestions("1. What does round.go:42 return for -2.5?\n2) Can March's total change?\n- Why half-to-even?\n- one\n- two\nNot a question.")
	if len(qs) != ship.QuizLength || qs[0].File != "round.go" || qs[0].Line != 42 || qs[1].Question != "Can March's total change?" || qs[2].Question != "Why half-to-even?" {
		t.Errorf("listed questions: %+v", qs)
	}
	if qs := ship.ParseQuestions("What changed?\nAnd why?"); len(qs) != 2 {
		t.Errorf("bare questions: %+v", qs)
	}
	for _, tt := range []struct {
		reply string
		want  ship.Verdict
	}{
		{`{"verdict": "Right", "note": "Right, including the line."}`, ship.Verdict{Verdict: core.VerdictRight, Note: "Right, including the line."}},
		{`{"verdict": "partly", "note": "Worth a look at 0042.sql before raising."}`, ship.Verdict{Verdict: core.VerdictPartly, Note: "Worth a look at 0042.sql before raising.", File: "0042.sql"}},
		{`{"verdict": "wrong", "note": "No.", "file": "currency.go", "line": 12}`, ship.Verdict{Verdict: core.VerdictWrong, Note: "No.", File: "currency.go", Line: 12}},
		{"✕ Wrong: see currency.go:12.", ship.Verdict{Verdict: core.VerdictWrong, Note: "Wrong: see currency.go:12.", File: "currency.go", Line: 12}},
		{"Correct.", ship.Verdict{Verdict: core.VerdictRight, Note: "Correct."}},
		{"Nothing to add, -2.5 rounds to -2.", ship.Verdict{Verdict: core.VerdictPartly, Note: "Nothing to add, -2.5 rounds to -2."}},
	} {
		if got := ship.ParseVerdict(tt.reply); got != tt.want {
			t.Errorf("%q: %+v, want %+v", tt.reply, got, tt.want)
		}
	}
}

// The quiz says it's writing the questions while the fork works, and once a reply has taken a while, how long.
func TestVerifyQuizWaiting(t *testing.T) {
	s, _, _, _ := verifyScene(t)
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	verifySettle(s, t, "the gates counted", func(f string) bool { return strings.Contains(f, "Verify…") && !strings.Contains(f, "counting") })
	s.ClickOn("Verify…")
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Midway(func() { s.WantFrame("Writing the questions…") })
	verifySettle(s, t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	answer(s, t, "It rounds to even")
	s.Frame()
	s.Now = s.Now.Add(4 * time.Second)
	s.Frame()
	if n := s.Node("Thinking"); n.Role != "status" {
		t.Errorf("the dots read as %+v", n)
	}
	s.Midway(func() { s.WantFrame("4s") })
	verifySettle(s, t, "the verdict", func(f string) bool { return !strings.Contains(f, "4s") })
}
