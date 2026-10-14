package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// An action card picks an action on a click of its button, and on its key while the card or a button in it has focus.
func TestActionCard(t *testing.T) {
	var id, allow ui.ID
	picked := -1
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		id = c.Next("card")
		res := ActionCard(c, "card", ActionCardOpts{Title: "Codex wants to run a command", Code: "$ go test ./...", Tone: col(c, tCardFg),
			Note: "rule: Bash(go test:*)", Help: "test/card",
			Actions: []CardAction{{Label: "Allow once", Key: "y", Primary: true}, {Label: "Deny", Key: "n"}, {Label: "Deny with note…", Key: "N"}}},
			func(c *ui.Ctx) {})
		allow = ui.Child(ui.Child(id, "foot0"), 0)
		if res.Action >= 0 {
			picked = res.Action
		}
	})
	h.frame()
	for _, s := range []string{"$ go test ./...", "rule: Bash(go test:*)", "Allow once", "y", "⇧n"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
	h.click(mid(h.rect(allow)))
	if picked != 0 {
		t.Fatalf("a click on Allow once picked %d", picked)
	}
	picked = -1
	h.keys("n")
	h.frame()
	if picked != 1 {
		t.Errorf("n on the focused button picked %d, want Deny", picked)
	}
	picked = -1
	h.u.Focus(id)
	h.frame()
	h.keys("+N")
	if picked != 2 {
		t.Errorf("⇧n on the card picked %d, want Deny with note", picked)
	}
}

func TestMeter(t *testing.T) {
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(200)}, func(c *ui.Ctx) {
			id = c.Next("m")
			Meter(c, "m", .25, MeterOpts{Tone: col(c, tCardFg), Label: "context"})
		})
	})
	h.frame()
	if n := h.access(id); n == nil || n.Name != "context 25%" {
		t.Errorf("its accessible node: %+v", n)
	}
	if r := h.rect(id); r.Dx() != 200 || r.Dy() != 4 {
		t.Errorf("rect %v", r)
	}
}
