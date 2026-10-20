package app

import (
	"context"
	"fmt"
	"image/color"

	"github.com/liamg/oriel/core"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/vim"
)

const explainBaseSrc = `package pricing

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v Decimal, p int32) Decimal {
	scaled := v.Shift(p)
	t := scaled.Truncate(0)
	switch scaled.Sub(t).Abs().Cmp(half) {
	case 1:
		return t.Add(sign(v)).Shift(-p)
	case 0:
		return t.Floor().Shift(-p)
	}
	return t.Shift(-p)
}

func RoundMinor(amt Money) (Money, error) {
	p, ok := currency.Precision(amt.Currency)
	if !ok {
		return Money{}, errUnknown
	}
	return Money{amt.Currency, roundHalfEven(amt.V, p)}, nil
}

func untouched() int { return 1 }
`

// explainWorkSrc is explainBaseSrc as the branch has it: lines 7, 8 and 12-15 in roundHalfEven, and 23 in RoundMinor.
const explainWorkSrc = `package pricing

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v Decimal, p int32) Decimal {
	scaled := v.Shift(p)
	t := scaled.Truncate(0)
	diff := scaled.Sub(t).Abs()
	switch diff.Cmp(half) {
	case 1:
		return t.Add(sign(v)).Shift(-p)
	case 0:
		if t.IntPart()%2 == 0 {
			return t.Shift(-p)
		}
		return t.Add(sign(v)).Shift(-p)
	}
	return t.Shift(-p)
}

func RoundMinor(amt Money) (Money, error) {
	p, ok := currency.Precision(amt.Currency)
	if !ok {
		return Money{}, fmt.Errorf("unknown currency %q", amt.Currency)
	}
	return Money{amt.Currency, roundHalfEven(amt.V, p)}, nil
}

func untouched() int { return 1 }
`

// fakeExplainer answers "Answer n (depth)" and keeps what it was asked.
type fakeExplainer struct {
	mu   sync.Mutex
	reqs []registry.ExplainRequest
}

func (f *fakeExplainer) explain(_ context.Context, r registry.ExplainRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	return fmt.Sprintf("Answer %d (%s)", len(f.reqs), r.Depth), nil
}

func (f *fakeExplainer) asked() []registry.ExplainRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// explainScene is round.go committed, then changed in the work tree, open in the editor, with a fake explainer.
func explainScene(t *testing.T) (*errScene, *fakeExplainer, string) {
	r := gittest.New(t)
	r.Write(map[string]string{"round.go": explainBaseSrc})
	r.Commit("Ada", "rounding")
	r.Write(map[string]string{"round.go": explainWorkSrc})
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	f := &fakeExplainer{}
	s.reg.Contribute(registry.Explainer{Name: "fake", Explain: f.explain, Label: func() string { return "fork of fake · knows this session" }})
	path := filepath.Join(r.Dir, "round.go")
	s.run("editor.open", registry.Args{"path": path})
	s.waitHighlights()
	s.explainSettle()
	return s, f, path
}

func (s *errScene) explainer() *explainPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*explainPlugin); ok {
			return x
		}
	}
	return nil
}

// explainSettle frames until the units, the base and the answers worked out off the loop are in.
func (s *errScene) explainSettle() {
	for range 4 {
		s.frame()
		s.sh.git.wait()
		s.explainer().wait()
		s.sh.runTodo()
	}
	s.frame()
}

// unitMarks are the lines (1-based) with a ? in the gutter, and each one's style.
func (s *errScene) unitMarks() map[int]string {
	x := s.editor()
	p := x.current()
	out := map[int]string{}
	for line, ds := range x.decorations(p, 0, p.Buf().Lines(), nil) {
		for _, d := range ds {
			if d.Kind == registry.Sign && d.Text == "?" {
				out[line+1] = d.Style
			}
		}
	}
	return out
}

// explainKeys types keys as the window does, through the keymaps and key layers.
func (s *errScene) explainKeys(keys ...string) {
	for _, k := range keys {
		s.keyCommand(s.t.(*testing.T), k)
		s.explainSettle()
	}
}

// RV-5: a ? on the smallest unit owning each change (ui.md 07b: "One hunk can touch three units, and one unit can
// span two hunks"); ␣e explains the unit at the cursor in a card under it with its breadcrumb; [ and ] widen and
// narrow, ␣e again widens, and the depths ask again.
// ref: oracle git
func TestExplainByUnit(t *testing.T) {
	s, f, _ := explainScene(t)
	var lines []int
	for l := range s.unitMarks() {
		lines = append(lines, l)
	}
	slices.Sort(lines)
	if want := []int{4, 8, 11, 12, 22}; !slices.Equal(lines, want) {
		t.Fatalf("? on lines %v, want %v (func roundHalfEven, the switch, case 0, the if in it, if !ok)", lines, want)
	}
	if !strings.Contains(s.frameText(), "explain by unit") || !strings.Contains(s.frameText(), "5 changed units · 3 hunks") {
		t.Errorf("the editor's explain action:\n%s", s.frameText())
	}

	s.cursorTo(13)
	s.explainKeys("<Space>e")
	s.wantText(t, "the card", "EXPLAIN", "func roundHalfEven", "switch diff.Cmp(half)", "case 0", "if t.IntPart()%2 == 0", "Answer 1 (brief)",
		"BRIEF", "DETAILED", "WHY · HISTORY", "fork of fake · knows this session", "Ask a follow-up", "Pin as note", "[ wider", "] narrower")
	reqs := f.asked()
	if len(reqs) != 1 || reqs[0].Kind != "unit" || !strings.Contains(reqs[0].Subject, "case 0 › if t.IntPart()%2 == 0") ||
		!strings.Contains(reqs[0].Context, "return t.Shift(-p)") {
		t.Fatalf("asked %+v", reqs)
	}
	if got := s.unitMarks()[12]; got != tExplainMarkOn.Name() {
		t.Errorf("the open unit's ? is %q", got)
	}

	s.explainKeys("[")
	s.wantText(t, "wider: case 0", "Answer 2 (brief)")
	if r := f.asked(); len(r) != 2 || !strings.HasSuffix(strings.Split(r[1].Subject, " (")[0], "› case 0") {
		t.Errorf("widening asked %+v", r[len(r)-1])
	}
	s.explainKeys("]")
	s.wantText(t, "narrower again, from the cache", "Answer 1 (brief)")
	s.explainKeys("<Space>e")
	s.wantText(t, "␣e again widens", "Answer 2 (brief)")
	if n := len(f.asked()); n != 2 {
		t.Errorf("asked %d times, want 2: the rest come from the cache", n)
	}

	s.clickText(t, "DETAILED")
	s.explainSettle()
	s.wantText(t, "detailed", "Answer 3 (detailed)")
	s.clickText(t, "WHY · HISTORY")
	s.explainSettle()
	reqs = f.asked()
	if last := reqs[len(reqs)-1]; last.Depth != registry.ExplainWhy || !strings.Contains(last.Context, "History (git blame") ||
		!strings.Contains(last.Context, "rounding") {
		t.Errorf("why · history asked %+v", last)
	}
	s.clickText(t, "func roundHalfEven")
	s.explainSettle()
	if last := f.asked()[len(f.asked())-1]; !strings.HasPrefix(last.Subject, "func roundHalfEven (") {
		t.Errorf("the crumb asked about %q", last.Subject)
	}

	s.explainKeys("<Esc>")
	if s.explainer().card != nil {
		t.Error("Escape didn't close the card")
	}
	if got := s.unitMarks()[12]; got != tUnitsDone.Name() {
		t.Errorf("the explained unit's ? is %q", got)
	}
}

// RV-5, done when: "cache invalidated only by edits inside the unit". An edit above the unit keeps its explanation;
// one inside marks it changed since, with a refresh that asks again.
func TestExplainCacheByContent(t *testing.T) {
	s, f, _ := explainScene(t)
	s.cursorTo(13)
	s.explainKeys("<Space>e")
	s.wantText(t, "the card", "Answer 1 (brief)")
	s.explainKeys("<Esc>")

	s.keys("ggO// Package pricing rounds money.<Esc>")
	s.explainSettle()
	if got := s.unitMarks()[13]; got != tUnitsDone.Name() {
		t.Errorf("after an edit above it, the unit's ? is %q, want explained", got)
	}
	s.cursorTo(14)
	s.explainKeys("<Space>e")
	s.wantText(t, "the cached card", "Answer 1 (brief)")
	if strings.Contains(s.frameText(), "changed since") {
		t.Error("an edit outside the unit marked it changed")
	}
	if n := len(f.asked()); n != 1 {
		t.Errorf("asked %d times after an edit elsewhere, want 1", n)
	}
	s.explainKeys("<Esc>")

	s.editor().e.SetCursor(vim.Pos{Line: 13})
	s.keys("A // even<Esc>")
	s.explainSettle()
	if got := s.unitMarks()[13]; got != tUnitsChanged.Name() {
		t.Errorf("after an edit inside it, the unit's ? is %q, want changed", got)
	}
	s.explainKeys("<Space>e")
	s.wantText(t, "the stale card", "Answer 1 (brief)", "changed since it was explained", "refresh")
	if n := len(f.asked()); n != 1 {
		t.Errorf("asked %d times before the refresh, want 1", n)
	}
	s.clickText(t, "refresh")
	s.explainSettle()
	s.wantText(t, "refreshed", "Answer 2 (brief)")
	if strings.Contains(s.frameText(), "changed since it was explained") {
		t.Error("still changed since after the refresh")
	}
}

// RV-5: in Visual mode ␣e explains the selection, under the units around it; the right-click menu has Explain this
// and the requests about the code.
func TestExplainThisOnASelection(t *testing.T) {
	s, f, _ := explainScene(t)
	s.cursorTo(22)
	s.keys("Vj")
	s.explainKeys("<Space>e")
	s.wantText(t, "the selection's card", "func RoundMinor", "selection · 2 lines", "Answer 1 (brief)")
	reqs := f.asked()
	if len(reqs) != 1 || reqs[0].Kind != "selection" || !strings.Contains(reqs[0].Context, `fmt.Errorf("unknown currency %q"`) {
		t.Fatalf("asked %+v", reqs)
	}
	if m := s.editor().e.Mode(); m != "normal" {
		t.Errorf("mode after explaining the selection: %s", m)
	}
	s.explainKeys("[")
	s.wantText(t, "wider than the selection", "Answer 2 (brief)")

	s.keys("V")
	at := registry.MenuCtx{View: EditorView, File: "round.go", Selection: true}
	var labels []string
	for _, it := range registry.MenuItems(s.reg, at) {
		if it.Group == "agent" {
			labels = append(labels, it.Label)
		}
	}
	if want := []string{"Explain this", "Ask about this…", "Review this", "Write tests for this", "Refactor this…"}; !slices.Equal(labels, want) {
		t.Errorf("the agent group: %q, want %q", labels, want)
	}
	text := s.rightClick(s.textAt(t, "Errorf"))
	for _, w := range []string{"Explain this", "␣ e", "Review this", "Write tests for this", "Refactor this…"} {
		if !strings.Contains(text, w) {
			t.Errorf("the menu lacks %q:\n%s", w, text)
		}
	}
	s.clickText(t, "Explain this")
	s.explainSettle()
	if n := len(f.asked()); n != 3 {
		t.Errorf("Explain this asked %d times in all, want 3", n)
	}
}

// RV-5: the Changed units panel lists every unit the branch touched, nested, with its explain status: changed since
// after an edit inside, explained again once the edit is undone.
func TestChangedUnitsPanel(t *testing.T) {
	s, _, _ := explainScene(t)
	s.cursorTo(13)
	s.explainKeys("<Space>e")
	s.explainKeys("<Esc>")
	s.cursorTo(13)
	s.keys("A // x<Esc>")
	s.explainSettle()
	s.cursorTo(24)
	s.explainKeys("<Space>e")
	s.run("explain.units", nil)
	s.explainSettle()
	s.explainSettle()
	s.wantText(t, "the panel", "CHANGED UNITS", "5 units", "round.go", "func roundHalfEven", "switch diff.Cmp(half)", "case 0",
		"if t.IntPart()%2 == 0", "func RoundMinor", "if !ok", "CHANGED SINCE", "OPEN", "–")
	files, _ := s.explainer().changedFiles()
	var rows []string
	for _, f := range files {
		for _, u := range f.units {
			rows = append(rows, u.label)
		}
	}
	if want := "func roundHalfEven|switch diff.Cmp(half)|case 0|if t.IntPart()%2 == 0|func RoundMinor|if !ok"; strings.Join(rows, "|") != want {
		t.Errorf("the panel's rows %q, want %q", rows, want)
	}
	s.keys("u")
	s.explainSettle()
	s.explainSettle()
	s.wantText(t, "the panel after the undo", "EXPLAINED ✓")
	s.lint("the explain card and the Changed units panel")
}

// RV-5: in a session the agent's fork answers (agents.md: "Side questions (btw, explain, verify quiz):
// $.model.fork: tool-less, outside the main conversation"), keys reach the card through the window, Pin as note is
// a resolved thread that never goes to the agent, and Ask a follow-up writes a request with the explanation.
func TestExplainAnsweredByTheSessionsFork(t *testing.T) {
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	if err := os.WriteFile(path, []byte(explainWorkSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	r.do(func() error { return r.host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.waitText("roundHalfEven")
	r.typeKeys("13G")
	r.waitFrame("the cursor on line 13", func(s string) bool { return strings.Contains(s, "\n13:") })
	r.typeKeys("<Space>e")
	r.waitFrame("the fork's answer", func(s string) bool {
		return containsAll(s, "EXPLAIN", "case 0", "if t.IntPart()%2 == 0", "fork says:", "fork of fake · knows this session")
	})
	r.typeKeys("[")
	r.waitFrame("wider", func(s string) bool {
		return containsAll(s, "case 0", "fork says:") && !strings.Contains(s, "Asking…")
	})
	a.NoneSent(t)

	r.clickText("Pin as note")
	th := waitThread(t, r, func(th core.Thread) bool { return strings.HasPrefix(th.Title, "Explain: ") })
	if th.Title != "Explain: func roundHalfEven › switch diff.Cmp(half) › case 0" || th.Status != core.ThreadResolved || th.Anchor == nil || th.Anchor.Line != 11 || th.Anchor.EndLine != 15 || !strings.Contains(th.Comments[0].Body, "fork says:") {
		t.Errorf("the note: %+v at %+v", th, th.Anchor)
	}
	a.NoneSent(t)

	r.clickText("Ask a follow-up")
	r.waitFrame("the request being written, with the explanation", func(s string) bool { return containsAll(s, "Write a request", "context") })
}

// waitThread waits for a thread of session w that ok says is the one.
func waitThread(t *testing.T, r *running, ok func(core.Thread) bool) core.Thread {
	t.Helper()
	var th core.Thread
	waitState(t, r.store, "the thread", func(st core.State) bool {
		se, found := st.Session("w")
		if !found {
			return false
		}
		for _, x := range se.Snapshot().Threads {
			if ok(x) {
				th = x
				return true
			}
		}
		return false
	})
	return th
}

// A run of git bars lines up down the gutter whatever else a line's sign column holds: round.go's ? marks share
// lines 8 and 12 with the bar (plugins.md, "block signs": "a run of them on consecutive lines is one unbroken bar").
func TestGitBarsLineUpBesideOtherSigns(t *testing.T) {
	s, _, _ := explainScene(t)
	s.frame()
	marks := s.unitMarks()
	if _, ok := marks[8]; !ok {
		t.Fatalf("no ? on line 8 to share it with the bar: %v", marks)
	}
	bar := theme.Hex(tokenColour(s.ui.Theme(), "git.modified", color.NRGBA{}))
	xs := map[string]int{}
	for _, l := range strings.Split(snapshot.List(s.list), "\n") {
		if f := strings.Fields(l); len(f) == 5 && f[0] == "rect" && f[4] == bar && strings.HasPrefix(f[2], "2.1") {
			xs[strings.Split(f[1], ",")[0]]++
		}
	}
	if len(xs) != 1 {
		t.Errorf("the git bars sit at %d places across: %v", len(xs), xs)
	}
}
