package widget

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func drawn(h *harness) string { return snapshot.List(h.list) }

// Busy says what's loading, adds what the load is doing, shows the elapsed time once it's been a while, and reads
// out as a status.
func TestBusy(t *testing.T) {
	var s LoadState
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 100}, func(c *ui.Ctx) {
		id = Busy(c, "b", s, BusyOpts{Label: "Checking which agents are installed", Help: "test/busy"})
	})
	s = LoadState{Phase: Pending, Since: h.clock.Now()}
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"Checking which agents are installed…"`) || strings.Contains(out, "0s") {
		t.Errorf("at once: %s", out)
	}
	if next := h.u.NextFrame(); !next.Equal(h.clock.Now().Add(3 * time.Second)) {
		t.Errorf("asked for a frame at %v, want when the clock shows", next)
	}
	h.clock.Advance(12 * time.Second)
	s.Note = "probing codex"
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"Checking which agents are installed… probing codex"`) || !strings.Contains(out, `"· 12s"`) {
		t.Errorf("after 12s: %s", out)
	}
	if n := h.access(id); n == nil || n.Name != "Checking which agents are installed… probing codex 12s" {
		t.Errorf("read out as: %+v", n)
	}
	h.clock.Advance(65 * time.Second)
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"· 1m 17s"`) {
		t.Errorf("after minutes: %s", out)
	}
}

// A known fraction fills that much of the track; an unknown one slides, and holds still and striped with reduced
// motion.
func TestProgressBar(t *testing.T) {
	tone := render.Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff}
	frac := .25
	build := func(c *ui.Ctx) {
		c.Column("p", ui.Style{W: ui.Px(400)}, func(c *ui.Ctx) {
			ProgressBar(c, "bar", ProgressOpts{Frac: frac, Height: 6, Tone: tone, Label: "cloning liamg/oriel", Help: "test/progress"})
		})
	}
	bars := func(h *harness) []render.Rect {
		var out []render.Rect
		for _, op := range h.list.Ops {
			if op.Kind == render.OpRect && op.Style.Fill == tone {
				out = append(out, op.Rect)
			}
		}
		return out
	}
	h := motionHarness(t, false, build)
	h.frame()
	if b := bars(h); len(b) != 1 || b[0].Dx() != 100 {
		t.Errorf("a quarter of 400px: %v", b)
	}
	if out := drawn(h); !strings.Contains(out, `"cloning liamg/oriel"`) || !strings.Contains(out, `"25%"`) {
		t.Errorf("the label and value: %s", out)
	}

	frac = -1
	h.frame()
	before := bars(h)
	h.clock.Advance(progressSweep / 4)
	h.frame()
	if after := bars(h); len(after) != 1 || len(before) != 1 || after[0] == before[0] {
		t.Errorf("the sliding bar didn't move: %v → %v", before, after)
	}

	still := motionHarness(t, true, build)
	frac = -1
	still.frame()
	b := bars(still)
	if len(b) != 0 {
		t.Errorf("a still bar is striped, not a solid segment: %v", b)
	}
	before2 := drawn(still)
	still.clock.Advance(progressSweep / 4)
	still.frame()
	if drawn(still) != before2 {
		t.Error("with reduced motion the bar moved")
	}
}

// Skeleton draws its rows at their widths, or its blocks, and reads out as loading.
func TestSkeleton(t *testing.T) {
	var rows, blocks ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 300}, func(c *ui.Ctx) {
		c.Column("p", ui.Style{W: ui.Px(200)}, func(c *ui.Ctx) {
			rows = Skeleton(c, "rows", SkeletonOpts{Rows: 3, Widths: []float32{1, .5}, Help: "test/skeleton"})
			blocks = Skeleton(c, "blocks", SkeletonOpts{Block: render.Point{X: 60, Y: 40}, Count: 2, Dir: ui.Row, Label: "agents", Help: "test/skeleton"})
		})
	})
	fill := resolved(t, theme.Dark).C(tSkeletonFill)
	var widths []float32
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && op.Style.Fill == fill {
			widths = append(widths, op.Rect.Dx())
		}
	}
	if len(widths) != 5 || widths[0] != 200 || widths[1] != 100 || widths[2] != 200 || widths[3] != 60 || widths[4] != 60 {
		t.Errorf("the placeholders' widths: %v", widths)
	}
	if n := h.access(rows); n == nil || n.Name != "Loading" {
		t.Errorf("rows read out as %+v", n)
	}
	if n := h.access(blocks); n == nil || n.Name != "agents" {
		t.Errorf("blocks read out as %+v", n)
	}
}

// A busy button can't be clicked, shows a spinner and its busy label, and keeps its width.
func TestButtonBusy(t *testing.T) {
	busy, clicks := false, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 100}, func(c *ui.Ctx) {
		id = c.Next("go")
		if Button(c, "go", "Start session", ButtonOpts{Primary: true, Busy: busy, BusyLabel: "Starting", Help: "test/button"}) {
			clicks++
			busy = true
		}
	})
	h.frame()
	w := h.rect(id).Dx()
	x, y := mid(h.rect(id))
	h.click(x, y)
	h.click(x, y)
	if clicks != 1 {
		t.Errorf("clicked %d times: a busy button took the second click", clicks)
	}
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"Starting…"`) {
		t.Errorf("busy label: %s", out)
	}
	if got := h.rect(id).Dx(); got < w {
		t.Errorf("width %v while busy, %v before", got, w)
	}
}

// Async shows nothing for its delay, then the loading view, which stays at least MinShow; then the body. A failure
// shows Retry; Ready and empty shows the empty text; a reload keeps the body with a refresh bar.
func TestAsync(t *testing.T) {
	var s LoadState
	empty, retried := false, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		id = Async(c, "a", s, AsyncOpts{Label: "Reading jobs", Empty: "No jobs yet", IsEmpty: empty, Retry: func() { retried++ }, Help: "test/async"},
			func(c *ui.Ctx) { c.Text("body", "the jobs", ui.TextStyle{}) })
	})
	start := h.clock.Now()
	s = LoadState{Phase: Pending, Since: start}
	h.frame()
	if out := drawn(h); strings.Contains(out, "Reading jobs") || strings.Contains(out, "the jobs") {
		t.Errorf("within the delay: %s", out)
	}
	if next := h.u.NextFrame(); !next.Equal(start.Add(asyncDelay)) {
		t.Errorf("woke at %v, want the end of the delay", next)
	}
	h.clock.Set(start.Add(200 * time.Millisecond))
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"Reading jobs…"`) {
		t.Errorf("after the delay: %s", out)
	}
	s = LoadState{Phase: Ready}
	h.clock.Set(start.Add(300 * time.Millisecond))
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"Reading jobs…"`) || strings.Contains(out, "the jobs") {
		t.Errorf("loaded before MinShow: %s", out)
	}
	h.clock.Set(start.Add(500 * time.Millisecond))
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"the jobs"`) || strings.Contains(out, "Reading jobs") {
		t.Errorf("after MinShow: %s", out)
	}

	// A fast load never shows the loading view.
	s = LoadState{Phase: Pending, Since: h.clock.Now(), Stale: true, Progress: .5}
	h.frame()
	if out := drawn(h); strings.Contains(out, "640x2") {
		t.Errorf("a refresh bar within the delay: %s", out)
	}
	h.clock.Advance(200 * time.Millisecond)
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"the jobs"`) {
		t.Errorf("reloading drops the body: %s", out)
	}
	bar := resolved(t, theme.Dark).C(tProgressBar)
	found := false
	for _, op := range h.list.Ops {
		found = found || op.Kind == render.OpRect && op.Style.Fill == bar && op.Rect.Dy() == 2 && op.Rect.Dx() == 320
	}
	if !found {
		t.Errorf("no refresh bar while reloading: %s", drawn(h))
	}

	s = LoadState{Phase: Failed, Err: errors.New("gh: not logged in")}
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"gh: not logged in"`) || !strings.Contains(out, `"Retry"`) {
		t.Fatalf("failed: %s", out)
	}
	h.click(mid(h.rect(ui.Child(ui.Child(id, "failed"), "retry"))))
	if retried != 1 {
		t.Errorf("Retry ran %d times", retried)
	}

	s, empty = LoadState{Phase: Ready}, true
	h.frame()
	if out := drawn(h); !strings.Contains(out, `"No jobs yet"`) || strings.Contains(out, "the jobs") {
		t.Errorf("empty: %s", out)
	}
}

func TestWaiting(t *testing.T) {
	ready := LoadState{Phase: Ready}
	if w := Waiting(ready, ready); w.Pending() || w.Failed() || w.Reason() != "" {
		t.Errorf("all ready: %v %v %q", w.Pending(), w.Failed(), w.Reason())
	}
	if w := Waiting(ready, LoadState{Phase: Pending, Note: "probing codex"}); !w.Pending() || w.Reason() != "still loading: probing codex" {
		t.Errorf("one pending: %q", w.Reason())
	}
	if w := Waiting(LoadState{Phase: Pending, Stale: true}); w.Pending() {
		t.Error("a reload with a value isn't waited on")
	}
	if w := Waiting(LoadState{Phase: Failed, Err: errors.New("boom")}, LoadState{}); !w.Failed() || w.Reason() != "couldn't load: boom" {
		t.Errorf("one failed: %q", w.Reason())
	}
}

// loadingSheet is every loading control in each of its states, for the gallery snapshot.
func loadingSheet(now time.Time) func(c *ui.Ctx) {
	pending := LoadState{Phase: Pending, Since: now.Add(-12 * time.Second), Progress: -1, Note: "probing codex"}
	return func(c *ui.Ctx) {
		th := c.Theme()
		c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20), Gap: 14, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
			Busy(c, "busy", LoadState{Phase: Pending, Since: now}, BusyOpts{Label: "Reading the stack", Help: "gallery/busy"})
			Busy(c, "slow", pending, BusyOpts{Label: "Checking which agents are installed", Help: "gallery/busy"})
			c.Row("bars", ui.Style{W: ui.Flex(1), Gap: 20}, func(c *ui.Ctx) {
				ProgressBar(c, "known", ProgressOpts{Frac: .62, Height: 6, Label: "cloning liamg/oriel", Width: ui.Px(260), Help: "gallery/progress"})
				ProgressBar(c, "unknown", ProgressOpts{Frac: -1, Height: 6, Label: "downloading the model", Width: ui.Px(260), Help: "gallery/progress"})
				ProgressBar(c, "edge", ProgressOpts{Frac: .3, Height: 2, Width: ui.Px(260), Help: "gallery/progress"})
			})
			c.Row("skeletons", ui.Style{W: ui.Flex(1), Gap: 20}, func(c *ui.Ctx) {
				Skeleton(c, "rows", SkeletonOpts{Rows: 4, Width: ui.Px(260), Help: "gallery/skeleton"})
				Skeleton(c, "cards", SkeletonOpts{Block: render.Point{X: 150, Y: 70}, Count: 3, Dir: ui.Row, Gap: 10, Help: "gallery/skeleton"})
			})
			c.Row("buttons", ui.Style{Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				Button(c, "start", "Start", ButtonOpts{Primary: true, Busy: true, BusyLabel: "Checking agents", Help: "gallery/button"})
				Button(c, "install", "Install", ButtonOpts{Busy: true, Help: "gallery/button"})
				Button(c, "ready", "Start", ButtonOpts{Primary: true, Key: "⌘↵", Help: "gallery/button"})
			})
			for i, p := range []Policy{Block, Gate, Optional, Inline} {
				name := [...]string{"Block", "Gate", "Optional", "Inline"}[i]
				c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Gap: 4, Fill: th.C(theme.BgSurface), Radius: 6}, func(c *ui.Ctx) {
					c.Text("name", name, uiText(c, 10, 600, th.C(theme.FgFaint)))
					Async(c, "a", pending, AsyncOpts{Policy: p, Label: "Reading models", Fallback: "the session's model", Delay: -1, Help: "gallery/async"}, nil)
				})
			}
			c.Row("states", ui.Style{W: ui.Flex(1), Gap: 20}, func(c *ui.Ctx) {
				c.Column("failed", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Fill: th.C(theme.BgSurface), Radius: 6}, func(c *ui.Ctx) {
					Async(c, "a", LoadState{Phase: Failed, Err: errors.New("codex: exec: not found")}, AsyncOpts{Retry: func() {}, Help: "gallery/async"}, nil)
				})
				c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Fill: th.C(theme.BgSurface), Radius: 6}, func(c *ui.Ctx) {
					Async(c, "a", LoadState{Phase: Ready}, AsyncOpts{Empty: "No jobs yet", IsEmpty: true, Help: "gallery/async"}, nil)
				})
				c.Column("refresh", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Fill: th.C(theme.BgSurface), Radius: 6}, func(c *ui.Ctx) {
					Async(c, "a", LoadState{Phase: Pending, Stale: true, Since: now.Add(-time.Second), Progress: .4}, AsyncOpts{Help: "gallery/async"}, func(c *ui.Ctx) {
						c.Text("t", "3 jobs, refreshing", uiText(c, 12, 400, th.C(theme.Fg)))
					})
				})
			})
		})
	}
}

func TestLoadingSnapshots(t *testing.T) {
	size := render.Point{X: 900, Y: 560}
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		h := newHarness(t, v, size, func(*ui.Ctx) {})
		h.build = loadingSheet(h.clock.Now())
		h.frame()
		h.frame()
		snapshot.Golden(t, "testdata/loading-"+string(v)+".list", []byte(drawn(h)))
		snapshot.GoldenPNG(t, "testdata/loading-"+string(v)+".png", raster.Render(h.list, size, 1, resolved(t, v).C(theme.BgBase)), 0.02, 0.001)
		if missing := h.u.WithoutHelp(); len(missing) > 0 {
			t.Errorf("%d interactive nodes have no help id", len(missing))
		}
	}
}
