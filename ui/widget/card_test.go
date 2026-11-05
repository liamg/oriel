package widget

import (
	"image/color"
	"testing"
	"time"

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

// A dismissable card's ✕, or Escape while it has focus, dismisses it; a countdown shows its ring and the time
// left.
func TestActionCardDismissAndCountdown(t *testing.T) {
	var id ui.ID
	dismissed := 0
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		id = c.Next("card")
		res := ActionCard(c, "card", ActionCardOpts{Kicker: "agentbus · question", Title: "tom's agent asks", Tone: col(c, tCardFg), Help: "test/card",
			Dismiss: true, Countdown: 2 * time.Minute, Left: 102 * time.Second, Actions: []CardAction{{Label: "Answer"}}}, nil)
		if res.Dismissed {
			dismissed++
		}
	})
	h.frame()
	if !h.drew("1:42") || !h.drew("✕") {
		t.Fatalf("no ring or ✕: %q", h.texts())
	}
	h.click(mid(h.rect(ui.Child(ui.Child(id, "kicker"), "dismiss"))))
	if dismissed != 1 {
		t.Fatalf("a click on ✕ dismissed %d times", dismissed)
	}
	h.u.Focus(id)
	h.frame()
	h.keys("Esc")
	if dismissed != 2 {
		t.Errorf("Escape on the card dismissed %d times, want once more", dismissed)
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

// A stacked meter draws its parts one after another, each its share of the width.
func TestMeterParts(t *testing.T) {
	var id ui.ID
	var read, skim color.NRGBA
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(200)}, func(c *ui.Ctx) {
			id = c.Next("m")
			read, skim = col(c, tCardFg), col(c, tMeterTrack)
			skim.R ^= 1
			Meter(c, "m", .5, MeterOpts{Tone: read, Height: 6, Label: "read", Then: []MeterPart{{Frac: .25, Tone: skim}}})
		})
	})
	widths := map[color.NRGBA]float32{}
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && (op.Style.Fill == read || op.Style.Fill == skim) {
			widths[op.Style.Fill] = op.Rect.Dx()
		}
	}
	if widths[read] != 100 || widths[skim] != 50 {
		t.Errorf("parts drawn %v wide, want 100 and 50", widths)
	}
	if r := h.rect(id); r.Dy() != 6 {
		t.Errorf("rect %v", r)
	}
	if n := h.access(id); n == nil || n.Name != "read 50%" {
		t.Errorf("its accessible node: %+v", n)
	}
}
