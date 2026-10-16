package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const invoiceBase = `package billing

// NewLine prices one metered line for an invoice.
func NewLine(m Meter, t Tier) (Line, error) {
	raw := t.Price(m.Qty)
	return Line{Meter: m.ID, Amount: raw}, nil
}
`

// invoiceWork is invoiceBase as the branch has it: lines 6-10 new.
const invoiceWork = `package billing

// NewLine prices one metered line for an invoice.
func NewLine(m Meter, t Tier) (Line, error) {
	raw := t.Price(m.Qty)
	amt, err := pricing.RoundMinor(raw)
	if err != nil {
		return Line{}, fmt.Errorf("line %s: %w", m.ID, err)
	}
	return Line{Meter: m.ID, Amount: amt, Rounding: pricing.HalfEven}, nil
}
`

// walkAnswer is a walkthrough as an agent might write it: a sentence first, a step naming its file by the end of its
// path, lines written three ways, a step with no lines, a summary, and trailing commas.
const walkAnswer = "Here's the walkthrough.\n\n```json\n" + `{"steps": [
 {"title": "Ties round to even", "project": "pricing", "file": "round.go", "lines": [4, 18],
  "prose": "The rounding function now looks at ` + "`diff`" + ` once.",
  "notes": [
   {"title": "Difference computed once", "lines": [7, 8], "text": "` + "`diff`" + ` is worked out before the switch."},
   {"title": "Ties go to even", "lines": "L11-15", "mark": "t.IntPart()%2 == 0", "text": "An even integer part rounds down."}]},
 {"title": "Invoices round each line once", "project": "pricing", "file": "billing/invoice.go", "lines": [4, 11],
  "prose": "Exactly one place: ` + "`NewLine`" + `.",
  "notes": [
   {"title": "Rounded once, at creation", "lines": [6, 9], "text": "` + "`RoundMinor`" + ` can fail."},
   {"title": "The mode is stored", "lines": [10], "mark": "Rounding: pricing.HalfEven", "text": "Each line records how it was rounded."}]},
 {"title": "The total sums rounded lines", "project": "billing-ui", "file": "Total.tsx", "prose": "The UI adds what the server rounded."},
 {"title": "Summary", "prose": "Lines round once, half to even, and remember how.",},
]}` + "\n```\n"

// fakeWalker answers every request with answer, and keeps what it was asked.
type fakeWalker struct {
	mu     sync.Mutex
	answer string
	reqs   []registry.ExplainRequest
}

func (f *fakeWalker) explain(_ context.Context, r registry.ExplainRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	return f.answer, nil
}

func (f *fakeWalker) asked() []registry.ExplainRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registry.ExplainRequest(nil), f.reqs...)
}

// walkScene is a session on two projects, pricing (round.go and internal/billing/invoice.go changed) and billing-ui
// (Total.tsx changed), with a fake explainer that writes walkAnswer.
func walkScene(t *testing.T) (s *errScene, f *fakeWalker, pricing, billing string) {
	t.Helper()
	p := gittest.New(t)
	p.Write(map[string]string{"round.go": explainBaseSrc, "internal/billing/invoice.go": invoiceBase})
	p.Commit("Ada", "rounding")
	p.Write(map[string]string{"round.go": explainWorkSrc, "internal/billing/invoice.go": invoiceWork})
	b := gittest.New(t)
	b.Write(map[string]string{"Total.tsx": "export const Total = ({ lines }) => sum(lines);\n"})
	b.Commit("Ada", "total")
	b.Write(map[string]string{"Total.tsx": "export const Total = ({ lines }) => sumRounded(lines);\n"})
	s = newScene(t, "", p.Dir, render.Point{X: 1600, Y: 1000})
	se := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindWork, Dir: p.Dir,
		Projects: []core.Project{{Name: "pricing", Repo: p.Dir, Dir: p.Dir, Branch: "rethink/rounding", Primary: true},
			{Name: "billing-ui", Repo: b.Dir, Dir: b.Dir, Branch: "rethink/rounding"}},
		Agent: core.AgentState{Provider: "claude"}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.sh.git.wait()
	f = &fakeWalker{answer: walkAnswer}
	s.reg.Contribute(registry.Explainer{Name: "fake", Explain: f.explain, Label: func() string { return "fork of fake · knows this session" },
		Byline: func() string { return "written by fake from the session's own context" }})
	return s, f, p.Dir, b.Dir
}

func (s *errScene) walker() *walkthroughPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*walkthroughPlugin); ok {
			return x
		}
	}
	return nil
}

// walkSettle frames until the walkthrough asked for off the loop is in.
func (s *errScene) walkSettle() {
	for range 4 {
		s.frame()
		s.walker().wait()
		s.sh.runTodo()
	}
	s.frame()
	s.frame()
}

func (s *errScene) walkKeys(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		s.keyCommand(t, k)
		s.frame()
	}
}

// glyphOpacity is how opaque the first run of text is drawn, through the layers it's in.
func (s *errScene) glyphOpacity(t *testing.T, text string) float32 {
	t.Helper()
	stack := []float32{1}
	for _, op := range s.list.Ops {
		switch op.Kind {
		case render.OpPushClip:
			stack = append(stack, stack[len(stack)-1])
		case render.OpPushOpacity:
			stack = append(stack, stack[len(stack)-1]*op.Amount)
		case render.OpPop:
			stack = stack[:len(stack)-1]
		case render.OpGlyphs:
			if op.Run.Text == text {
				return stack[len(stack)-1]
			}
		}
	}
	t.Fatalf("no %q in the frame", text)
	return 0
}

// RV-6 (07-explain, ui.md: "A step dock with ← → across files and repos. Code outside the step dims. Notes pinned to
// ranges with leader lines"): Explain this change asks the explainer for the branch's change in every project, and
// walks through its answer a step at a time.
func TestExplainChangeWalkthrough(t *testing.T) {
	s, f, _, _ := walkScene(t)
	s.run("explain.change", nil)
	s.walkSettle()
	reqs := f.asked()
	if len(reqs) != 1 {
		t.Fatalf("asked %d times", len(reqs))
	}
	r := reqs[0]
	if r.Kind != "change" || r.Format != walkFormat || r.Subject != "The branch's change: 3 files in 2 projects" || len(r.Files) != 3 {
		t.Errorf("asked %+v", r)
	}
	for _, want := range []string{"## project pricing · round.go (modified)", "## project pricing · internal/billing/invoice.go (modified)",
		"## project billing-ui · Total.tsx (modified)", "+   12 | \t\tif t.IntPart()%2 == 0 {", "    5 | \tscaled := v.Shift(p)",
		"-      | \t\treturn Money{}, errUnknown", "+    1 | export const Total = ({ lines }) => sumRounded(lines);"} {
		if !strings.Contains(r.Context, want) {
			t.Errorf("the context lacks %q:\n%s", want, r.Context)
		}
	}

	s.wantText(t, "step 1", "EXPLAIN THIS CHANGE", "pricing / round.go", "L4–18", "throwaway · discarded if the code changes", "Discard",
		"DIFFERENCE COMPUTED ONCE", "TIES GO TO EVEN", "STEP", "01", "/04", "Ties round to even", "diff", "Ask about this step", "Comment on L7",
		"written by fake from the session's own context", "billing-ui", "Total.tsx", "summary", "2 repos · 3 files",
		"EXPLAIN", "step 1/4", "←/→ step · c comment · q quit")
	if lit, dim := s.glyphOpacity(t, "7"), s.glyphOpacity(t, "20"); lit != 1 || dim >= .5 {
		t.Errorf("line 7 (a note's) drawn at %v, line 20 (outside) at %v: want it lit and the rest dimmed", lit, dim)
	}

	s.walkKeys(t, "<Right>")
	s.wantText(t, "step 2, by →", "pricing / internal/billing/invoice.go", "L4–11", "ROUNDED ONCE, AT CREATION", "THE MODE IS STORED",
		"Invoices round each line once", "Comment on L6", "step 2/4", "invoice.go")
	s.walkKeys(t, "j", "j")
	s.wantText(t, "j moves the line commented on", "Comment on L8")

	s.clickText(t, "Total.tsx")
	s.walkSettle()
	s.wantText(t, "step 3, by its chip", "billing-ui / Total.tsx", "The total sums rounded lines", "step 3/4")
	s.walkKeys(t, "<Right>")
	s.wantText(t, "the summary", "This step is about the change as a whole.", "Lines round once, half to even", "/04")
	if strings.Contains(s.frameText(), "Comment on L") {
		t.Error("the summary offers a comment on a line")
	}
	s.clickText(t, "←")
	s.walkSettle()
	s.wantText(t, "← goes back", "billing-ui / Total.tsx")
	s.lint("the walkthrough")

	s.walkKeys(t, "q")
	if s.showing(ExplainChangeView) {
		t.Fatal("q didn't close the walkthrough")
	}
	s.run("explain.change", nil)
	s.walkSettle()
	s.wantText(t, "reopened where it was", "billing-ui / Total.tsx", "step 3/4")
	if n := len(f.asked()); n != 1 {
		t.Errorf("reopening asked again: %d requests", n)
	}
}

// RV-6: "throwaway · discarded if the code changes": an edit outside every step's lines keeps the walkthrough, and
// one inside a step's (in the editor, or on disk) throws it away.
func TestExplainChangeIsThrowaway(t *testing.T) {
	s, f, pricing, billing := walkScene(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.run("explain.change", nil)
	s.walkSettle()
	s.wantText(t, "the walkthrough", "Ties round to even")
	s.clickText(t, "Discard")
	s.walkSettle()
	if s.walker().w != nil || s.showing(ExplainChangeView) {
		t.Fatal("Discard kept the walkthrough")
	}
	s.run("explain.change", nil)
	s.walkSettle()

	s.cursorTo(27)
	s.keys("A // still one<Esc>")
	s.run("explain.change", nil)
	s.walkSettle()
	if s.walker().w == nil || !s.showing(ExplainChangeView) {
		t.Fatal("an edit outside every step threw the walkthrough away")
	}

	s.cursorTo(13)
	s.keys("A // even<Esc>")
	s.run("explain.change", nil)
	s.walkSettle()
	if n := len(f.asked()); n != 3 {
		t.Fatalf("after an edit inside step 1, explain.change asked %d times in all, want 3: a new walkthrough", n)
	}
	if !strings.Contains(s.fullText(), "discarded: round.go changed") {
		t.Errorf("no notice of the discard:\n%s", s.fullText())
	}

	if err := os.WriteFile(filepath.Join(billing, "Total.tsx"), []byte("export const Total = ({ lines }) => 0;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.now = s.now.Add(2 * time.Second)
	s.walkSettle()
	if s.walker().w != nil || s.showing(ExplainChangeView) {
		t.Error("a change on disk to step 3's file kept the walkthrough")
	}

}

// RV-6: the omnibox finds Explain this change; an answer that isn't a walkthrough says so, with Try again; ␣E in the
// editor opens the walkthrough again.
func TestExplainChangeFromTheOmniboxAndKeys(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	f.answer = "I'd rather chat about it."
	s.run("omnibox.open", registry.Args{"query": "explain this change"})
	s.frame()
	s.frame()
	s.wantText(t, "the omnibox", "COMMANDS", ": a walkthrough of the branch's change across every project and file")
	s.walkKeys(t, "<CR>")
	s.walkSettle()
	s.wantText(t, "an answer with no steps", "Try again")
	if !strings.Contains(s.fullText(), `the answer has no walkthrough in it (its steps should be JSON in a `+"```json"+` block); it began "I'd rather chat about it."`) {
		t.Errorf("the error:\n%s", s.fullText())
	}
	f.answer = walkAnswer
	s.clickText(t, "Try again")
	s.walkSettle()
	s.wantText(t, "tried again", "Ties round to even")

	s.walkKeys(t, "q")
	s.run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.frame()
	s.walkKeys(t, "<Space>E")
	s.walkSettle()
	if !s.showing(ExplainChangeView) {
		t.Fatal("␣E didn't open the walkthrough")
	}
	s.wantText(t, "␣E", "Ties round to even", "step 1/4")
	if n := len(f.asked()); n != 2 {
		t.Errorf("asked %d times, want 2", n)
	}
}

// RV-6: Ask about this step writes a request with the step attached, and Comment on L<n> (c) opens a thread on the
// line in the editor.
func TestExplainChangeAskAndComment(t *testing.T) {
	s, _, pricing, _ := walkScene(t)
	var got []string
	var args []registry.Args
	for _, id := range []string{"request.new", "thread.new"} {
		s.reg.Command(registry.Command{ID: id, Run: func(_ registry.Ctx, a registry.Args) error {
			got, args = append(got, id), append(args, a)
			return nil
		}})
	}
	s.run("explain.change", nil)
	s.walkSettle()
	s.clickText(t, "Ask about this step")
	s.walkSettle()
	if len(got) != 1 || got[0] != "request.new" || args[0]["file"] != filepath.Join(pricing, "round.go") ||
		!strings.Contains(args[0].String("context"), "Step 1 of 4 of the walkthrough of this change: Ties round to even") ||
		!strings.Contains(args[0].String("context"), "  12  \t\tif t.IntPart()%2 == 0 {") {
		t.Fatalf("Ask about this step ran %v %v", got, args)
	}
	s.walkKeys(t, "c")
	if len(got) != 2 || got[1] != "thread.new" || args[1]["file"] != filepath.Join(pricing, "round.go") || args[1].Int("line") != 7 {
		t.Fatalf("c ran %v %v", got, args)
	}
	if p := s.editor().current(); p.Name() == "" || p.Cursor().Line != 6 {
		t.Errorf("the editor isn't on round.go's line 7: %q at %+v", p.Name(), p.Cursor())
	}
}

// RV-6 (07-explain): the walkthrough against a golden.
func TestExplainChangeSnapshot(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.run("explain.change", nil)
	s.walkSettle()
	s.walkKeys(t, "<Right>")
	s.walkSettle()
	s.golden("explain-change")
}

// RV-6: a walkthrough is read out of an answer however it's wrapped, its lines written any of the ways an agent
// writes them; an answer with none is an error.
func TestParseWalkthrough(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		want         string // each step as title@start-end with its notes' spans
	}{
		{"fenced, with prose around it", walkAnswer,
			"Ties round to even@4-18[7-8 11-15]|Invoices round each line once@4-11[6-9 10-10]|The total sums rounded lines@0-0[]|Summary@0-0[]"},
		{"bare JSON", `{"steps": [{"title": "One", "file": "a.go", "start": 3, "end": 1, "prose": "p"}]}`, "One@1-3[]"},
		{"a list of steps", "```\n[{\"title\": \"One\", \"file\": \"a.go\", \"lines\": 5, \"notes\": [{\"title\": \"n\", \"lines\": \"L6–9\"}]}]\n```",
			"One@5-5[6-9]"},
		{"JSON inside prose", `Sure! {"walkthrough": [{"title": "One", "text": "body"}]} Hope that helps.`, "One@0-0[]"},
		{"empty notes and steps dropped", `{"steps": [{"title": ""}, {"title": "One", "notes": [{}]}]}`, "One@0-0[]"},
	} {
		steps, err := parseWalkthrough(tc.answer)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var got []string
		for _, s := range steps {
			var ns []string
			for _, n := range s.Notes {
				ns = append(ns, fmt.Sprintf("%d-%d", n.Start, n.End))
			}
			got = append(got, fmt.Sprintf("%s@%d-%d[%s]", s.Title, s.Start, s.End, strings.Join(ns, " ")))
		}
		if g := strings.Join(got, "|"); g != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, g, tc.want)
		}
	}
	for _, bad := range []string{"", "No steps here.", "```json\n{\"steps\": []}\n```", "{not json"} {
		if _, err := parseWalkthrough(bad); err == nil {
			t.Errorf("%q read as a walkthrough", bad)
		}
	}
}

// RV-6: the explainer reads each file's change with the file's line numbers as it is now, removed lines unnumbered,
// three lines of context, and ⋯ between far-apart changes.
func TestNumberedDiff(t *testing.T) {
	base := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	file := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nl\nm\n"
	got, first, last := numberedDiff([]byte(base), []byte(file))
	want := "     1 | a\n-      | b\n+    2 | B\n     3 | c\n     4 | d\n     5 | e\n  ⋯\n     8 | h\n     9 | i\n    10 | j\n-      | k\n    11 | l\n+   12 | m\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if first != 2 || last != 12 {
		t.Errorf("changed lines %d-%d, want 2-12", first, last)
	}
}

// RV-6: in a session the walkthrough is written by the agent's fork (agents.md: "Side questions (btw, explain,
// verify quiz): $.model.fork: tool-less, outside the main conversation"), told the answer's format, and nothing
// reaches the main conversation.
func TestExplainChangeAskedOfTheSessionsFork(t *testing.T) {
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	if err := os.WriteFile(filepath.Join(dir, "round.go"), []byte(explainWorkSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.do(func() error { return r.host.Run(context.Background(), "explain.change", nil) }); err != nil {
		t.Fatal(err)
	}
	// The fake fork echoes the end of its prompt, which is the format, and that isn't a walkthrough.
	r.waitFrame("the fork's answer", func(s string) bool {
		return containsAll(s, "EXPLAIN THIS CHANGE", "fork says:", walkFormat[len(walkFormat)-25:], "Try again")
	})
	a.NoneSent(t)
}

// RV-6: the Changed units panel offers the walkthrough of the whole change.
func TestExplainChangeFromChangedUnits(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.run("explain.units", nil)
	s.explainSettle()
	s.explainSettle()
	s.clickText(t, "Explain this change, step by step")
	s.walkSettle()
	s.wantText(t, "the walkthrough", "EXPLAIN THIS CHANGE", "Ties round to even")
	if n := len(f.asked()); n != 1 {
		t.Errorf("asked %d times", n)
	}
}

// RV-6: the diff view's header has Explain this change (plugins.md, "A diff view's header button runs explain.change").
func TestExplainChangeFromTheDiff(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	s.run("diff.open", registry.Args{"path": pricing})
	s.until(t, "the diff", func() bool { s.frame(); return strings.Contains(s.frameText(), "CHANGES") })
	s.clickText(t, "Explain this change")
	s.walkSettle()
	s.wantText(t, "the walkthrough", "EXPLAIN THIS CHANGE", "Ties round to even")
	if n := len(f.asked()); n != 1 {
		t.Errorf("asked %d times", n)
	}
}

// RV-6: a Lua explainer gets the request's format and can give a byline (scripting.md, oriel.agent.explainer).
func TestLuaExplainerFormatAndByline(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.agent.explainer{ name = "local", order = 5, byline = "written by a local model",
  run = function(req) return req.kind .. ": " .. req.format end }
`)
	md, e, err := registry.Explain(context.Background(), s.reg, registry.ExplainRequest{Kind: "change", Format: "JSON steps"})
	if err != nil || md != "change: JSON steps" || e.WrittenBy() != "written by a local model" {
		t.Errorf("answered %q by %q, %v", md, e.WrittenBy(), err)
	}
}
