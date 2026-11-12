package explain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/registry"
)

// A step dock with ← → across files and repos, code outside the step dimmed, and notes pinned to ranges with leader
// lines: Explain this change asks the explainer for the branch's change in every project, and walks through its
// answer a step at a time.
func TestExplainChangeWalkthrough(t *testing.T) {
	s, f, _, _ := walkScene(t)
	s.Run("explain.change", nil)
	walkSettle(s)
	reqs := f.Asked()
	if len(reqs) != 1 {
		t.Fatalf("asked %d times", len(reqs))
	}
	r := reqs[0]
	if r.Kind != "change" || r.Format != walkFormatText() || r.Subject != "The branch's change: 3 files in 2 projects" || len(r.Files) != 3 {
		t.Errorf("asked %+v", r)
	}
	for _, want := range []string{"## project pricing · round.go (modified)", "## project pricing · internal/billing/invoice.go (modified)",
		"## project billing-ui · Total.tsx (modified)", "+   12 | \t\tif t.IntPart()%2 == 0 {", "    5 | \tscaled := v.Shift(p)",
		"-      | \t\treturn Money{}, errUnknown", "+    1 | export const Total = ({ lines }) => sumRounded(lines);"} {
		if !strings.Contains(r.Context, want) {
			t.Errorf("the context lacks %q:\n%s", want, r.Context)
		}
	}

	wantText(t, s, "step 1", "EXPLAIN THIS CHANGE", "pricing / round.go", "L4–18", "throwaway · discarded if the code changes", "Discard",
		"DIFFERENCE COMPUTED ONCE", "TIES GO TO EVEN", "STEP", "01", "/04", "Ties round to even", "diff", "Ask about this step", "Comment on L7",
		"written by fake from the session's own context", "billing-ui", "Total.tsx", "summary", "2 repos · 3 files",
		"EXPLAIN", "step 1/4", "←/→ step · c comment · q quit")
	if lit, dim := glyphOpacity(t, s, "7"), glyphOpacity(t, s, "20"); lit != 1 || dim >= .5 {
		t.Errorf("line 7 (a note's) drawn at %v, line 20 (outside) at %v: want it lit and the rest dimmed", lit, dim)
	}

	walkKeys(s, "<Right>")
	wantText(t, s, "step 2, by →", "pricing / internal/billing/invoice.go", "L4–11", "ROUNDED ONCE, AT CREATION", "THE MODE IS STORED",
		"Invoices round each line once", "Comment on L6", "step 2/4", "invoice.go")
	walkKeys(s, "j", "j")
	wantText(t, s, "j moves the line commented on", "Comment on L8")

	s.ClickTop("Total.tsx")
	walkSettle(s)
	wantText(t, s, "step 3, by its chip", "billing-ui / Total.tsx", "The total sums rounded lines", "step 3/4")
	walkKeys(s, "<Right>")
	wantText(t, s, "the summary", "This step is about the change as a whole.", "Lines round once, half to even", "/04")
	if strings.Contains(s.Text(), "Comment on L") {
		t.Error("the summary offers a comment on a line")
	}
	s.ClickTop("←")
	walkSettle(s)
	wantText(t, s, "← goes back", "billing-ui / Total.tsx")
	s.Lint("the walkthrough")

	walkKeys(s, "q")
	if s.Showing(explain.ExplainChangeView) {
		t.Fatal("q didn't close the walkthrough")
	}
	s.Run("explain.change", nil)
	walkSettle(s)
	wantText(t, s, "reopened where it was", "billing-ui / Total.tsx", "step 3/4")
	if n := len(f.Asked()); n != 1 {
		t.Errorf("reopening asked again: %d requests", n)
	}
}

// Throwaway, discarded if the code changes: an edit outside every step's lines keeps the walkthrough, and
// one inside a step's (in the editor, or on disk) throws it away.
func TestExplainChangeIsThrowaway(t *testing.T) {
	s, f, pricing, billing := walkScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.Run("explain.change", nil)
	walkSettle(s)
	wantText(t, s, "the walkthrough", "Ties round to even")
	s.ClickTop("Discard")
	walkSettle(s)
	if walker(s).Held() || s.Showing(explain.ExplainChangeView) {
		t.Fatal("Discard kept the walkthrough")
	}
	s.Run("explain.change", nil)
	walkSettle(s)

	cursorTo(s, 27)
	s.EditorKeys("A // still one<Esc>")
	s.Run("explain.change", nil)
	walkSettle(s)
	if !walker(s).Held() || !s.Showing(explain.ExplainChangeView) {
		t.Fatal("an edit outside every step threw the walkthrough away")
	}

	cursorTo(s, 13)
	s.EditorKeys("A // even<Esc>")
	s.Run("explain.change", nil)
	walkSettle(s)
	if n := len(f.Asked()); n != 3 {
		t.Fatalf("after an edit inside step 1, explain.change asked %d times in all, want 3: a new walkthrough", n)
	}
	if !strings.Contains(s.FullText(), "discarded: round.go changed") {
		t.Errorf("no notice of the discard:\n%s", s.FullText())
	}

	if err := os.WriteFile(filepath.Join(billing, "Total.tsx"), []byte("export const Total = ({ lines }) => 0;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Until("a change on disk to step 3's file to end the walkthrough", func() bool {
		walkSettle(s)
		return !walker(s).Held() && !s.Showing(explain.ExplainChangeView)
	})

}

// The omnibox finds Explain this change; an answer that isn't a walkthrough says so, with Try again; ␣E in the
// editor opens the walkthrough again.
func TestExplainChangeFromTheOmniboxAndKeys(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	f.SetAnswer("I'd rather chat about it.")
	s.Run("omnibox.open", registry.Args{"query": "explain this change"})
	s.Frame()
	s.Frame()
	wantText(t, s, "the omnibox", "COMMANDS", "explain.change", "Explain this change: a walkthrough")
	walkKeys(s, "<CR>")
	walkSettle(s)
	wantText(t, s, "an answer with no steps", "Try again")
	if !strings.Contains(s.FullText(), `the answer has no walkthrough in it (its steps should be JSON in a `+"```json"+` block); it began "I'd rather chat about it."`) {
		t.Errorf("the error:\n%s", s.FullText())
	}
	f.SetAnswer(rig.WalkAnswer)
	s.ClickTop("Try again")
	walkSettle(s)
	wantText(t, s, "tried again", "Ties round to even")

	walkKeys(s, "q")
	s.Run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.Frame()
	walkKeys(s, "<Space>E")
	walkSettle(s)
	if !s.Showing(explain.ExplainChangeView) {
		t.Fatal("␣E didn't open the walkthrough")
	}
	wantText(t, s, "␣E", "Ties round to even", "step 1/4")
	if n := len(f.Asked()); n != 2 {
		t.Errorf("asked %d times, want 2", n)
	}
}

// Ask about this step writes a request with the step attached, and Comment on L<n> (c) opens a thread on the
// line in the editor.
func TestExplainChangeAskAndComment(t *testing.T) {
	s, _, pricing, _ := walkScene(t)
	var got []string
	var args []registry.Args
	for _, id := range []string{"request.new", "thread.new"} {
		s.Registry().Command(registry.Command{ID: id, Run: func(_ registry.Ctx, a registry.Args) error {
			got, args = append(got, id), append(args, a)
			return nil
		}})
	}
	s.Run("explain.change", nil)
	walkSettle(s)
	s.ClickTop("Ask about this step")
	walkSettle(s)
	if len(got) != 1 || got[0] != "request.new" || args[0]["file"] != filepath.Join(pricing, "round.go") ||
		!strings.Contains(args[0].String("context"), "Step 1 of 4 of the walkthrough of this change: Ties round to even") ||
		!strings.Contains(args[0].String("context"), "  12  \t\tif t.IntPart()%2 == 0 {") {
		t.Fatalf("Ask about this step ran %v %v", got, args)
	}
	walkKeys(s, "c")
	if len(got) != 2 || got[1] != "thread.new" || args[1]["file"] != filepath.Join(pricing, "round.go") || args[1].Int("line") != 7 {
		t.Fatalf("c ran %v %v", got, args)
	}
	if w := s.EditorWindow(); w.Path == "" || w.Cursor.Line != 6 {
		t.Errorf("the editor isn't on round.go's line 7: %q at %+v", w.Path, w.Cursor)
	}
}

// The walkthrough against a golden.
func TestExplainChangeSnapshot(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.Run("explain.change", nil)
	walkSettle(s)
	walkKeys(s, "<Right>")
	walkSettle(s)
	s.SettleGit()
	s.Golden("explain-change")
}

// In a session the walkthrough is written by the agent's fork ($.model.fork: tool-less, outside the main
// conversation), told the answer's format, and nothing reaches the main conversation.
func TestExplainChangeAskedOfTheSessionsFork(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := r.ProjectDir("w")
	if err := os.WriteFile(filepath.Join(dir, "round.go"), []byte(rig.ExplainWork), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Run("explain.change", nil)
	// The fake fork echoes the end of its prompt, which is the format, and that isn't a walkthrough.
	r.WaitFrame("the fork's answer", func(s string) bool {
		return apptest.HasAll(s, "EXPLAIN THIS CHANGE", "fork says:", walkFormatText()[len(walkFormatText())-25:], "Try again")
	})
	a.NoneSent(t)
}

// The Changed units panel offers the walkthrough of the whole change.
func TestExplainChangeFromChangedUnits(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(pricing, "round.go")})
	s.Run("explain.units", nil)
	settle(s)
	settle(s)
	s.ClickTop("Explain this change, step by step")
	walkSettle(s)
	wantText(t, s, "the walkthrough", "EXPLAIN THIS CHANGE", "Ties round to even")
	if n := len(f.Asked()); n != 1 {
		t.Errorf("asked %d times", n)
	}
}

// The diff view's header has Explain this change, a button that runs explain.change.
func TestExplainChangeFromTheDiff(t *testing.T) {
	s, f, pricing, _ := walkScene(t)
	s.Run("diff.open", registry.Args{"path": pricing})
	s.Until("the diff", func() bool { s.Frame(); return strings.Contains(s.Text(), "Explain this change") })
	s.ClickTop("Explain this change")
	walkSettle(s)
	wantText(t, s, "the walkthrough", "EXPLAIN THIS CHANGE", "Ties round to even")
	if n := len(f.Asked()); n != 1 {
		t.Errorf("asked %d times", n)
	}
}

// A Lua explainer (oriel.agent.explainer) gets the request's format and can give a byline.
func TestLuaExplainerFormatAndByline(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.agent.explainer{ name = "local", order = 5, byline = "written by a local model",
  run = function(req) return req.kind .. ": " .. req.format end }
`)
	md, e, err := registry.Explain(context.Background(), s.Registry(), registry.ExplainRequest{Kind: "change", Format: "JSON steps"})
	if err != nil || md != "change: JSON steps" || e.WrittenBy() != "written by a local model" {
		t.Errorf("answered %q by %q, %v", md, e.WrittenBy(), err)
	}
}

// walkFormatText is the walkthrough format as Oriel ships it.
func walkFormatText() string {
	d, _ := prompts.Lookup(explain.WalkFormat)
	return d.Default
}
