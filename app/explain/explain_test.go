package explain_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// A ? on the smallest unit owning each change (one hunk can touch three units, and one unit can span two hunks); ␣e
// explains the unit at the cursor in a card under it with its breadcrumb; [ and ] widen and narrow, ␣e again widens,
// and the depths ask again.
// ref: oracle git
func TestExplainByUnit(t *testing.T) {
	s, f, _ := explainScene(t)
	var lines []int
	for l := range unitMarks(s) {
		lines = append(lines, l)
	}
	slices.Sort(lines)
	if want := []int{4, 8, 11, 12, 22}; !slices.Equal(lines, want) {
		t.Fatalf("? on lines %v, want %v (func roundHalfEven, the switch, case 0, the if in it, if !ok)", lines, want)
	}
	if !strings.Contains(s.Text(), "explain by unit") || !strings.Contains(s.Text(), "5 changed units · 3 hunks") {
		t.Errorf("the editor's explain action:\n%s", s.Text())
	}

	cursorTo(s, 13)
	explainKeys(s, "<Space>e")
	wantText(t, s, "the card", "EXPLAIN", "func roundHalfEven", "switch diff.Cmp(half)", "case 0", "if t.IntPart()%2 == 0", "Answer 1 (brief)",
		"BRIEF", "DETAILED", "WHY · HISTORY", "fork of fake · knows this session", "Ask a follow-up", "Pin as note", "[ wider", "] narrower")
	reqs := f.Asked()
	if len(reqs) != 1 || reqs[0].Kind != "unit" || !strings.Contains(reqs[0].Subject, "case 0 › if t.IntPart()%2 == 0") ||
		!strings.Contains(reqs[0].Context, "return t.Shift(-p)") {
		t.Fatalf("asked %+v", reqs)
	}
	if got := unitMarks(s)[12]; got != explain.MarkOpen() {
		t.Errorf("the open unit's ? is %q", got)
	}

	explainKeys(s, "[")
	wantText(t, s, "wider: case 0", "Answer 2 (brief)")
	if r := f.Asked(); len(r) != 2 || !strings.HasSuffix(strings.Split(r[1].Subject, " (")[0], "› case 0") {
		t.Errorf("widening asked %+v", r[len(r)-1])
	}
	explainKeys(s, "]")
	wantText(t, s, "narrower again, from the cache", "Answer 1 (brief)")
	explainKeys(s, "<Space>e")
	wantText(t, s, "␣e again widens", "Answer 2 (brief)")
	if n := len(f.Asked()); n != 2 {
		t.Errorf("asked %d times, want 2: the rest come from the cache", n)
	}

	s.ClickTop("DETAILED")
	settle(s)
	wantText(t, s, "detailed", "Answer 3 (detailed)")
	s.ClickTop("WHY · HISTORY")
	settle(s)
	reqs = f.Asked()
	if last := reqs[len(reqs)-1]; last.Depth != registry.ExplainWhy || !strings.Contains(last.Context, "History (git blame") ||
		!strings.Contains(last.Context, "rounding") {
		t.Errorf("why · history asked %+v", last)
	}
	s.ClickTop("func roundHalfEven")
	settle(s)
	if last := f.Asked()[len(f.Asked())-1]; !strings.HasPrefix(last.Subject, "func roundHalfEven (") {
		t.Errorf("the crumb asked about %q", last.Subject)
	}

	explainKeys(s, "<Esc>")
	if cardOpen(s) {
		t.Error("Escape didn't close the card")
	}
	if got := unitMarks(s)[12]; got != explain.MarkExplained() {
		t.Errorf("the explained unit's ? is %q", got)
	}
}

// The cache is invalidated only by edits inside the unit. An edit above the unit keeps its explanation;
// one inside marks it changed since, with a refresh that asks again.
func TestExplainCacheByContent(t *testing.T) {
	s, f, _ := explainScene(t)
	cursorTo(s, 13)
	explainKeys(s, "<Space>e")
	wantText(t, s, "the card", "Answer 1 (brief)")
	explainKeys(s, "<Esc>")

	s.EditorKeys("ggO// Package pricing rounds money.<Esc>")
	settle(s)
	if got := unitMarks(s)[13]; got != explain.MarkExplained() {
		t.Errorf("after an edit above it, the unit's ? is %q, want explained", got)
	}
	cursorTo(s, 14)
	explainKeys(s, "<Space>e")
	wantText(t, s, "the cached card", "Answer 1 (brief)")
	if strings.Contains(s.Text(), "changed since") {
		t.Error("an edit outside the unit marked it changed")
	}
	if n := len(f.Asked()); n != 1 {
		t.Errorf("asked %d times after an edit elsewhere, want 1", n)
	}
	explainKeys(s, "<Esc>")

	s.Editor().SetCursor(text.Pos{Line: 13})
	s.EditorKeys("A // even<Esc>")
	settle(s)
	if got := unitMarks(s)[13]; got != explain.MarkChanged() {
		t.Errorf("after an edit inside it, the unit's ? is %q, want changed", got)
	}
	explainKeys(s, "<Space>e")
	wantText(t, s, "the stale card", "Answer 1 (brief)", "changed since it was explained", "refresh")
	if n := len(f.Asked()); n != 1 {
		t.Errorf("asked %d times before the refresh, want 1", n)
	}
	s.ClickTop("refresh")
	settle(s)
	wantText(t, s, "refreshed", "Answer 2 (brief)")
	if strings.Contains(s.Text(), "changed since it was explained") {
		t.Error("still changed since after the refresh")
	}
}

// In Visual mode ␣e explains the selection, under the units around it; the right-click menu has Explain this
// and the requests about the code.
func TestExplainThisOnASelection(t *testing.T) {
	s, f, _ := explainScene(t)
	cursorTo(s, 22)
	s.EditorKeys("Vj")
	explainKeys(s, "<Space>e")
	wantText(t, s, "the selection's card", "func RoundMinor", "selection · 2 lines", "Answer 1 (brief)")
	reqs := f.Asked()
	if len(reqs) != 1 || reqs[0].Kind != "selection" || !strings.Contains(reqs[0].Context, `fmt.Errorf("unknown currency %q"`) {
		t.Fatalf("asked %+v", reqs)
	}
	if m := s.Editor().Mode(); m != "normal" {
		t.Errorf("mode after explaining the selection: %s", m)
	}
	explainKeys(s, "[")
	wantText(t, s, "wider than the selection", "Answer 2 (brief)")

	s.EditorKeys("V")
	at := registry.MenuCtx{View: kit.EditorView, File: "round.go", Selection: true}
	var labels []string
	for _, it := range registry.MenuItems(s.Registry(), at) {
		if it.Group == "agent" {
			labels = append(labels, it.Label)
		}
	}
	if want := []string{"Explain this", "Ask about this…", "Review this", "Write tests for this", "Refactor this…"}; !slices.Equal(labels, want) {
		t.Errorf("the agent group: %q, want %q", labels, want)
	}
	text := s.RightClick(s.TextPoint("Errorf"))
	for _, w := range []string{"Explain this", "␣ e", "Review this", "Write tests for this", "Refactor this…"} {
		if !strings.Contains(text, w) {
			t.Errorf("the menu lacks %q:\n%s", w, text)
		}
	}
	s.ClickTop("Explain this")
	settle(s)
	if n := len(f.Asked()); n != 3 {
		t.Errorf("Explain this asked %d times in all, want 3", n)
	}
}

// The Changed units panel lists every unit the branch touched, nested, with its explain status: changed since
// after an edit inside, explained again once the edit is undone.
func TestChangedUnitsPanel(t *testing.T) {
	s, _, _ := explainScene(t)
	cursorTo(s, 13)
	explainKeys(s, "<Space>e")
	explainKeys(s, "<Esc>")
	cursorTo(s, 13)
	s.EditorKeys("A // x<Esc>")
	settle(s)
	cursorTo(s, 24)
	explainKeys(s, "<Space>e")
	s.Run("explain.units", nil)
	settle(s)
	settle(s)
	wantText(t, s, "the panel", "CHANGED UNITS", "5 units", "round.go", "func roundHalfEven", "switch diff.Cmp(half)", "case 0",
		"if t.IntPart()%2 == 0", "func RoundMinor", "if !ok", "CHANGED SINCE", "OPEN", "–")
	rows := explainer(s).ChangedRows()
	if want := "func roundHalfEven|switch diff.Cmp(half)|case 0|if t.IntPart()%2 == 0|func RoundMinor|if !ok"; strings.Join(rows, "|") != want {
		t.Errorf("the panel's rows %q, want %q", rows, want)
	}
	s.EditorKeys("u")
	settle(s)
	settle(s)
	wantText(t, s, "the panel after the undo", "EXPLAINED ✓")
	s.Lint("the explain card and the Changed units panel")
}

// In a session the agent's fork answers ($.model.fork: tool-less, outside the main conversation), keys reach the
// card through the window, Pin as note is a resolved thread that never goes to the agent, and Ask a follow-up writes
// a request with the explanation.
func TestExplainAnsweredByTheSessionsFork(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	path := filepath.Join(r.ProjectDir("w"), "round.go")
	if err := os.WriteFile(path, []byte(rig.ExplainWork), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Run("editor.open", registry.Args{"path": path})
	r.WaitText("roundHalfEven")
	r.TypeKeys("13G")
	r.WaitFrame("the cursor on line 13", func(s string) bool { return strings.Contains(s, "\n13:") })
	r.TypeKeys("<Space>e")
	r.WaitFrame("the fork's answer", func(s string) bool {
		return apptest.HasAll(s, "EXPLAIN", "case 0", "if t.IntPart()%2 == 0", "fork says:", "fork of fake · knows this session")
	})
	r.TypeKeys("[")
	r.WaitFrame("wider", func(s string) bool {
		return apptest.HasAll(s, "case 0", "fork says:") && !strings.Contains(s, "Asking…")
	})
	a.NoneSent(t)

	r.ClickText("Pin as note")
	th := waitThread(t, r, func(th core.Thread) bool { return strings.HasPrefix(th.Title, "Explain: ") })
	if th.Title != "Explain: func roundHalfEven › switch diff.Cmp(half) › case 0" || th.Status != core.ThreadResolved || th.Anchor == nil || th.Anchor.Line != 11 || th.Anchor.EndLine != 15 || !strings.Contains(th.Comments[0].Body, "fork says:") {
		t.Errorf("the note: %+v at %+v", th, th.Anchor)
	}
	a.NoneSent(t)

	r.ClickText("Ask a follow-up")
	r.WaitFrame("the request being written, with the explanation", func(s string) bool { return apptest.HasAll(s, "Write a request", "context") })
}
