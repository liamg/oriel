package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A section is its kicker, note and body over a rule; its stats read as their name and value.
func TestSectionAndStats(t *testing.T) {
	var stat, ago ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 300}, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(300)}, func(c *ui.Ctx) {
			Section(c, "usage", "Usage", "today", func(c *ui.Ctx) {
				stat = c.Next("cost")
				Stat(c, "cost", "Cost", "$1.20", c.Theme().C(theme.FgMuted))
				ago = c.Next("seen")
				StatAgo(c, "seen", "Seen", time.Unix(1000, 0).Add(-2*time.Minute), " ago", c.Theme().C(theme.FgMuted))
			})
		})
	})
	for _, want := range []string{"USAGE", "today", "Cost", "$1.20", "Seen"} {
		if !h.drew(want) {
			t.Errorf("drew %q, want %q", h.texts(), want)
		}
	}
	if a := h.access(stat); a == nil || a.Name != "Cost $1.20" {
		t.Errorf("the stat reads %+v", a)
	}
	if a := h.access(ago); a == nil || a.Name != "Seen 2m ago" {
		t.Errorf("the ago stat reads %+v", a)
	}
}
