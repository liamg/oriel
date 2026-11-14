package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Ago says how long ago in short, moves on as time does, and shows the full time once the pointer rests on it.
func TestAgo(t *testing.T) {
	var h *harness
	at := time.Unix(1000, 0).Add(-5*time.Minute - 10*time.Second)
	h = newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10)}, func(c *ui.Ctx) { Ago(c, "ago", at, AgoOpts{Text: uiText(c, 12, 400, col(c, tLabel))}) })
	})
	if !h.drew("5m") {
		t.Fatalf("drew %q, want 5m", h.texts())
	}
	if next := h.u.NextFrame(); !next.Equal(at.Add(6 * time.Minute)) {
		t.Errorf("next frame at %v, want when it turns 6m", next)
	}
	h.clock.Advance(50 * time.Second)
	h.frame()
	if !h.drew("6m") {
		t.Errorf("a minute on, drew %q", h.texts())
	}
	h.pointer(render.PointerMove, 0, 14, 16)
	h.frame()
	h.clock.Advance(2 * time.Second)
	h.frame()
	if !strings.Contains(strings.Join(h.texts(), "|"), Timestamp(at)) {
		t.Errorf("no tooltip with the full time %q: %q", Timestamp(at), h.texts())
	}
}

func TestAgoLabels(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for _, c := range []struct {
		ago   time.Duration
		short string
		long  string
	}{
		{20 * time.Second, "now", "just now"},
		{5 * time.Minute, "5m", "5m ago"},
		{3 * time.Hour, "3h", "3h ago"},
		{50 * time.Hour, "2d", "2d ago"},
		{15 * 24 * time.Hour, "2w", "2w ago"},
	} {
		if got := AgoLabel(now, now.Add(-c.ago)); got != c.short {
			t.Errorf("AgoLabel %v = %q, want %q", c.ago, got, c.short)
		}
		if got := AgoLong(now, now.Add(-c.ago)); got != c.long {
			t.Errorf("AgoLong %v = %q, want %q", c.ago, got, c.long)
		}
	}
}

// An AgoLine joins its parts, skipping empty ones, and each time in it has its own tooltip.
func TestAgoLine(t *testing.T) {
	now := time.Unix(1000, 0)
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		AgoLine(c, "l", AgoLineOpts{Text: uiText(c, 12, 400, col(c, tLabel))},
			AgoPart{Text: "main"}, AgoPart{}, AgoPart{Text: "last commit ", At: now.Add(-2 * time.Hour), Long: true, Suffix: " by liam"},
			AgoPart{Text: "used ", At: now.Add(-3 * time.Minute), Long: true})
	})
	got := strings.Join(h.texts(), "")
	if want := "main · last commit 2h ago by liam · used 3m ago"; got != want {
		t.Errorf("drew %q, want %q", got, want)
	}
}
