package widget

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// promptRig is a prompt card in a window of size, with detail lines; picked is the option it last reported.
type promptRig struct {
	h      *harness
	id     ui.ID
	picked int
	gone   int
	scope  int
}

func newPromptRig(t *testing.T, size render.Point, lines int, why string) *promptRig {
	t.Helper()
	r := &promptRig{picked: -1}
	var detail []PromptLine
	for i := range lines {
		detail = append(detail, PromptLine{Lead: fmt.Sprintf("%4d ", i+1), Text: fmt.Sprintf("line %d of the edit", i+1)})
	}
	r.h = newHarness(t, theme.Dark, size, func(c *ui.Ctx) {
		r.id = c.Next("prompt")
		res := PromptCard(c, "prompt", PromptCardOpts{Kicker: "rounding · permission", Count: "1 of 3", Title: "Claude wants to edit round.go",
			Where: "rounding · main · lines 46–48", Why: why, Tone: col(c, tCardFg), Detail: detail, DetailLabel: "The edit", Help: "test/prompt",
			Dismiss: true, Scopes: []string{"proxy.golang.org", "*.golang.org"}, Scope: &r.scope,
			Options: []PromptOption{{Key: "y", Label: "Allow once"}, {Key: "a", Label: "Allow for this session", Note: "Edit"},
				{Key: "A", Label: "Allow for all sessions", Note: "Edit · every session"}, {Key: "n", Label: "Deny", Danger: true},
				{Key: "N", Label: "Deny with a note…", Danger: true}}})
		if res.Option >= 0 {
			r.picked = res.Option
		}
		if res.Dismissed {
			r.gone++
		}
	})
	r.h.frame()
	r.h.u.Focus(r.id)
	r.h.frame()
	return r
}

func (r *promptRig) detail() ui.ID {
	return ui.Child(ui.Child(ui.Child(r.id, "detailbox"), "frame"), "detail")
}

func (r *promptRig) option(i int) ui.ID {
	return ui.Child(ui.Child(ui.Child(r.id, "options"), "list"), i)
}

// The answers are a list: ↑/↓ move the highlight and ↵ picks it, an option's key picks it at once, and a click
// picks a row. Each row is a list item named with its key and the rule it makes; ←/→ choose a scope.
func TestPromptCardAnswers(t *testing.T) {
	r := newPromptRig(t, render.Point{X: 1000, Y: 700}, 4, "")
	h := r.h
	h.keys("Down", "Down", "Up", "Down", "CR")
	if r.picked != 2 {
		t.Fatalf("↓↓↑↓↵ picked %d, want 2 (Allow for all sessions)", r.picked)
	}
	r.picked = -1
	h.keys("+N")
	if r.picked != 4 {
		t.Errorf("⇧N picked %d, want Deny with a note", r.picked)
	}
	r.picked = -1
	h.keys("a")
	if r.picked != 1 {
		t.Errorf("a picked %d, want Allow for this session", r.picked)
	}
	r.picked = -1
	h.click(mid(h.rect(r.option(3))))
	if r.picked != 3 {
		t.Errorf("a click on Deny picked %d", r.picked)
	}
	h.u.Focus(r.id)
	h.frame()
	h.keys("Right")
	if r.scope != 1 {
		t.Errorf("→ chose scope %d, want *.golang.org", r.scope)
	}
	h.keys("Esc")
	if r.gone != 1 {
		t.Errorf("Escape dismissed it %d times", r.gone)
	}
	list := h.access(ui.Child(ui.Child(r.id, "options"), "list"))
	if list == nil || list.Role != render.RoleList || len(list.Children) != 5 {
		t.Fatalf("the answers aren't a list of five: %+v", list)
	}
	for i, want := range []string{"Allow once, y", "Allow for this session, a, Edit", "Allow for all sessions, ⇧A, Edit · every session",
		"Deny, n", "Deny with a note…, ⇧N"} {
		if n := list.Children[i]; n.Role != render.RoleListItem || n.Name != want {
			t.Errorf("answer %d is %s %q, want a list item %q", i, n.Role, n.Name, want)
		}
	}
	for _, s := range []string{"ROUNDING · PERMISSION", "1 OF 3", "⇧A", "Edit · every session"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q", s)
		}
	}
}

// A detail of thousands of lines scrolls inside a box no taller than 40% of the window: the card fits the window
// with its answers in view, and the wheel, ⇧↓ and PgDn scroll the detail, not the answers' highlight.
func TestPromptCardLongDetailScrollsInside(t *testing.T) {
	size := render.Point{X: 1000, Y: 700}
	r := newPromptRig(t, size, 3000, "")
	h := r.h
	h.frame()
	card, detail := h.rect(r.id), h.rect(r.detail())
	if card.Dy() > size.Y-2*promptMargin+1 {
		t.Errorf("the card is %v tall in a %v window", card.Dy(), size.Y)
	}
	if detail.Dy() > size.Y*promptDetail+1 {
		t.Errorf("the detail is %v tall, past 40%% of %v", detail.Dy(), size.Y)
	}
	for i := range 5 {
		if o := h.rect(r.option(i)); o.Max.Y > card.Max.Y || o.Max.Y > size.Y {
			t.Errorf("answer %d at %v is past the card (%v) or the window", i, o, card)
		}
	}
	if !strings.Contains(strings.Join(h.texts(), "\n"), "more lines · ⇧↓ PgDn") {
		t.Errorf("nothing says there's more below: %q", h.texts())
	}
	h.keys("+Down")
	after := h.u.Scroll(r.detail()).Y
	if after <= 0 {
		t.Fatal("⇧↓ didn't scroll the detail")
	}
	h.keys("PageDown")
	if paged := h.u.Scroll(r.detail()).Y; paged <= after {
		t.Errorf("PgDn didn't scroll on from %v (now %v)", after, paged)
	}
	h.keys("CR")
	if r.picked != 0 {
		t.Errorf("⇧↓ and PgDn moved the highlight: ↵ picked %d", r.picked)
	}
	before := h.u.Scroll(r.detail()).Y
	h.wheel(render.Point{X: (detail.Min.X + detail.Max.X) / 2, Y: (detail.Min.Y + detail.Max.Y) / 2}, 120)
	if got := h.u.Scroll(r.detail()).Y; got <= before {
		t.Errorf("the wheel didn't scroll the detail: %v, then %v", before, got)
	}
	r.picked = -1
	h.click(mid(h.rect(r.option(1))))
	if r.picked != 1 {
		t.Errorf("with the detail scrolled, a click on an answer picked %d", r.picked)
	}

	// In a short window the detail gives way, and the answers stay in it.
	short := render.Point{X: 800, Y: 520}
	h.size = short
	h.frame()
	h.frame()
	card = h.rect(r.id)
	if last := h.rect(r.option(4)); last.Max.Y > card.Max.Y || card.Max.Y > short.Y || card.Dy() > short.Y-2*promptMargin+1 {
		t.Errorf("in a %v window the card is %v and its last answer %v", short, card, last)
	}
}

// What led to the ask shows three lines, the rest behind "more".
func TestPromptCardWhyClamps(t *testing.T) {
	why := strings.Repeat("Floor rounds toward negative infinity, so negative halves go the wrong way; switching to Truncate. ", 8)
	r := newPromptRig(t, render.Point{X: 1000, Y: 700}, 2, why)
	h := r.h
	if !h.drew("WHY") || !h.drew("more") {
		t.Fatalf("no WHY or no more: %q", h.texts())
	}
	lines := func() int {
		n := 0
		for _, s := range h.texts() {
			if strings.Contains(s, "Truncate") || strings.Contains(s, "Floor") || strings.Contains(s, "negative") {
				n++
			}
		}
		return n
	}
	if n := lines(); n > promptWhyMax {
		t.Errorf("%d lines of why shown, want %d", n, promptWhyMax)
	}
	moreID := ui.Child(ui.Child(ui.Child(ui.Child(r.id, "why"), "box"), "text"), "more")
	h.click(mid(h.rect(moreID)))
	h.frame()
	if n := lines(); n <= promptWhyMax || !h.drew("less") {
		t.Errorf("more showed %d lines: %q", n, h.texts())
	}
}

// Keys typed as a prompt that takes focus appears answer nothing.
func TestPromptCardIgnoresKeysAsItAppears(t *testing.T) {
	picked := -1
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 1000, Y: 600}, func(c *ui.Ctx) {
		id = c.Next("p")
		if res := PromptCard(c, "p", PromptCardOpts{Title: "t", TakeFocus: true, Tone: col(c, tCardFg),
			Options: []PromptOption{{Key: "y", Label: "Allow once"}}}); res.Option >= 0 {
			picked = res.Option
		}
	})
	h.frame()
	h.u.Focus(id)
	h.frame()
	h.keys("y")
	if picked != -1 {
		t.Fatal("a key typed as it appeared answered it")
	}
	h.now = h.now.Add(CardKeyGuard)
	h.frame()
	h.keys("y")
	if picked != 0 {
		t.Errorf("y once it settled picked %d", picked)
	}
}

// An action that isn't an answer is a small button in the head, its key picking it too; the foot is drawn under the
// answers.
func TestPromptCardActionsAndFoot(t *testing.T) {
	action := -1
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 1000, Y: 600}, func(c *ui.Ctx) {
		id = c.Next("p")
		res := PromptCard(c, "p", PromptCardOpts{Title: "t", Tone: col(c, tCardFg), Options: []PromptOption{{Key: "y", Label: "Allow once"}},
			Actions: []CardAction{{Label: "Console", Key: "g"}}, Foot: func(c *ui.Ctx) { c.Text("note", "a note field", uiText(c, 12, 400, col(c, tCardFg))) }})
		if res.Action >= 0 {
			action = res.Action
		}
	})
	h.frame()
	if !h.drew("Console") || !h.drew("a note field") {
		t.Fatalf("no Console button or foot: %q", h.texts())
	}
	h.u.Focus(id)
	h.frame()
	h.keys("g")
	if action != 0 {
		t.Errorf("g picked action %d", action)
	}
}
