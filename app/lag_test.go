//go:build !race

package app_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/tools/perf"
)

// The lag budgets drive the real loop as a fast mouse does: moves at 1 kHz, and every so often a jump onto something
// that lights up under the pointer (a probe), timed until a frame shows it lit. They fail when a probe takes too long,
// when probes take longer at the end of a run than at its start, or when the loop can't take moves as fast as they come.
const (
	moveEvery  = time.Millisecond
	probeP90   = 50 * time.Millisecond
	probeMax   = 150 * time.Millisecond
	sendBehind = 30 * time.Millisecond // how far a burst of moves may fall behind its schedule
)

// lagRepo is a repo with enough files to fill the Explorer.
func lagRepo(t *testing.T) string {
	r := gittest.New(t)
	files := map[string]string{}
	for i := range 80 {
		files[fmt.Sprintf("pkg/f%02d.go", i)] = "package pkg\n"
	}
	r.Write(files)
	r.Commit("Ada", "base")
	return r.Dir
}

// lagApp is the app on a repo with the Explorer open on All, settled.
func lagApp(t *testing.T) *apptest.Window {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: lagRepo(t)})
	r.Do(func() error {
		return r.Host.Run(context.Background(), "view.toggle", registry.Args{"id": kit.ExplorerView})
	})
	r.Do(func() error { return r.Host.Run(context.Background(), "explorer.scope", registry.Args{"scope": "all"}) })
	r.WaitText("pkg")
	r.ClickText("pkg")
	r.WaitText("f25.go")
	settleWindow(r)
	return r
}

// clearOfBlinks waits until Ori, in the sidebar, won't blink for d: its blinks draw frames of their own.
func clearOfBlinks(d time.Duration) {
	for {
		now := time.Now()
		shut, next := agentui.OriBlink(now)
		if !shut && next.Sub(now) >= d {
			return
		}
		time.Sleep(next.Sub(now) + agentui.OriBlinkFor)
	}
}

// settleWindow waits until no frame has been drawn for a moment.
func settleWindow(r *apptest.Window) {
	for n := -1; ; {
		time.Sleep(150 * time.Millisecond)
		if m := len(r.Win.Frames()); m == n {
			return
		} else {
			n = m
		}
	}
}

// windowTextPoint is the middle of the run reading text in the frame on screen.
func windowTextPoint(r *apptest.Window, text string) render.Point {
	r.T.Helper()
	var at render.Point
	r.Latest(`"`+text+`"`, func(f headless.Frame) bool {
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == text {
				at = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
				return true
			}
		}
		return false
	})
	return at
}

// topFill is the fill of the last rect drawn under p.
func topFill(f headless.Frame, p render.Point) render.Color {
	var got render.Color
	for _, op := range f.Ops {
		r := op.Rect
		if op.Kind == render.OpRect && op.Style.Fill.A > 0 && p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y {
			got = op.Style.Fill
		}
	}
	return got
}

// lagRun is what a run of moves and probes measured.
type lagRun struct {
	probes []time.Duration
	behind time.Duration // the furthest a burst of moves fell behind its schedule
}

// sendMoves sends the pointer along path, one point every moveEvery, and says how far it fell behind: the loop not
// taking moves as fast as they come.
func sendMoves(r *apptest.Window, path []render.Point, buttons render.Buttons) time.Duration {
	start := time.Now()
	var behind time.Duration
	for i, p := range path {
		due := start.Add(time.Duration(i) * moveEvery)
		if d := time.Until(due); d > 0 {
			time.Sleep(d)
		}
		r.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p, Buttons: buttons})
		behind = max(behind, time.Since(due))
	}
	return behind
}

// probeAt jumps the pointer to p, and times it until a frame draws something else under p.
func probeAt(r *apptest.Window, p render.Point) time.Duration {
	r.T.Helper()
	fs := r.Win.Frames()
	before, n := topFill(fs[len(fs)-1], p), len(fs)
	start := time.Now()
	r.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	for time.Since(start) < 2*time.Second {
		fs := r.Win.Frames()
		for _, f := range fs[n:] {
			if topFill(f, p) != before {
				return time.Since(start)
			}
		}
		n = len(fs)
		time.Sleep(200 * time.Microsecond)
	}
	r.T.Fatalf("nothing lit up under %v in 2s", p)
	return 0
}

// sweep is a back-and-forth line between a and b, n points long.
func sweep(a, b render.Point, n int) []render.Point {
	path := make([]render.Point, n)
	for i := range path {
		f := float32(i%40) / 40
		if (i/40)%2 == 1 {
			f = 1 - f
		}
		path[i] = render.Point{X: a.X + (b.X-a.X)*f, Y: a.Y + (b.Y-a.Y)*f}
	}
	return path
}

// measureLag sweeps noise for 200 ms then probes each target in turn, rounds times, and checks the run against the budgets.
func measureLag(r *apptest.Window, name string, noise []render.Point, targets []render.Point, rounds int) lagRun {
	r.T.Helper()
	var run lagRun
	for i := range rounds {
		run.behind = max(run.behind, sendMoves(r, noise, 0))
		run.probes = append(run.probes, probeAt(r, targets[i%len(targets)]))
	}
	checkLag(r, name, run)
	return run
}

func checkLag(r *apptest.Window, name string, run lagRun) {
	r.T.Helper()
	sorted := slices.Clone(run.probes)
	slices.Sort(sorted)
	p90, worst := sorted[len(sorted)*9/10], sorted[len(sorted)-1]
	third := len(run.probes) / 3
	median := func(d []time.Duration) time.Duration {
		d = slices.Clone(d)
		slices.Sort(d)
		return d[len(d)/2]
	}
	first, last := median(run.probes[:third]), median(run.probes[len(run.probes)-third:])
	r.T.Logf("%s: %d probes, p90 %v, worst %v, first third %v, last third %v; moves fell %v behind", name, len(run.probes),
		p90, worst, first, last, run.behind)
	if p90 > probeP90 || worst > probeMax {
		r.T.Errorf("%s: the frame showing what's under the pointer took %v (p90), %v at worst; the budget is %v, %v", name, p90, worst, probeP90, probeMax)
	}
	if last > 2*first+10*time.Millisecond {
		r.T.Errorf("%s: probes slowed down over the run: %v at the start, %v at the end", name, first, last)
	}
	if run.behind > sendBehind {
		r.T.Errorf("%s: the loop fell %v behind moves at 1 kHz", name, run.behind)
	}
}

// Sweeping the Explorer's rows, each row the pointer lands on lights up within budget, as quickly at the end of a
// long run as at its start.
func TestExplorerHoverLagBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	top, mid := windowTextPoint(r, "f00.go"), windowTextPoint(r, "f10.go")
	noise := sweep(top, mid, 200)
	var targets []render.Point
	for _, f := range []string{"f20.go", "f14.go", "f25.go", "f17.go", "f22.go"} {
		targets = append(targets, windowTextPoint(r, f))
	}
	measureLag(r, "explorer", noise, targets, 60)
}

// The same after drags over the Explorer: a drag that ends mustn't leave moves building frames they didn't before,
// or slow the ones that do.
func TestHoverLagAfterDragBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	row := windowTextPoint(r, "f00.go")
	along := func() int {
		sendMoves(r, []render.Point{row}, 0)
		settleWindow(r)
		clearOfBlinks(2 * time.Second)
		n := len(r.Win.Frames())
		sendMoves(r, sweep(row, render.Point{X: row.X + 30, Y: row.Y}, 200), 0)
		settleWindow(r)
		return len(r.Win.Frames()) - n
	}
	clean := along()
	a, b := windowTextPoint(r, "f02.go"), windowTextPoint(r, "f12.go")
	for range 5 {
		r.Win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: a, Buttons: render.ButtonPrimary})
		sendMoves(r, sweep(a, b, 80), render.ButtonPrimary)
		r.Win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: b, Buttons: render.ButtonPrimary})
	}
	r.Win.Send(render.KeyEvent{Key: "Esc"})
	settleWindow(r)
	if n := along(); n > clean+3 {
		t.Errorf("200 moves along one row built %d frames after drags, %d before", n, clean)
	}
	var targets []render.Point
	for _, f := range []string{"f20.go", "f14.go", "f25.go"} {
		targets = append(targets, windowTextPoint(r, f))
	}
	measureLag(r, "after drags", sweep(row, windowTextPoint(r, "f10.go"), 200), targets, 30)
}

// A context menu over the Explorer: its items light up within budget while the pointer keeps moving.
func TestMenuHoverLagBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	at := windowTextPoint(r, "f05.go")
	r.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	r.Win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonSecondary})
	r.Win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonSecondary})
	r.WaitText("Close")
	settleWindow(r)
	first, second, third := windowTextPoint(r, "Move pane…"), windowTextPoint(r, "Close"), windowTextPoint(r, "? Help")
	measureLag(r, "menu", sweep(first, render.Point{X: first.X + 60, Y: first.Y}, 200), []render.Point{second, third}, 40)
}

// With --trace, a stall writes the execution trace of the seconds before it, for go tool trace.
func TestSlowFrameWritesTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stall.trace")
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: t.TempDir(), Config: func(c *apptest.Config) { c.Trace = path }})
	r.Win.Send(render.SlowFrameEvent{Waited: 900 * time.Millisecond})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 1<<10 {
			if !bytes.HasPrefix(b, []byte("go 1.")) {
				t.Fatalf("not a trace: %q", b[:16])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no trace written for the slow frame")
}

// Hovering the Explorer over thousands of changed files: rows light up within budget from the start, the files'
// diffs not made until they're opened.
func TestExplorerHoverLagOverManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	dir := rig.ChangedRepo(t, 4000)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: dir})
	r.Do(func() error {
		return r.Host.Run(context.Background(), "view.toggle", registry.Args{"id": kit.ExplorerView})
	})
	r.WaitText("f1080.txt")
	var targets []render.Point
	for _, f := range []string{"f1020.txt", "f040.txt", "f1080.txt", "f060.txt", "f1040.txt"} {
		targets = append(targets, windowTextPoint(r, f))
	}
	measureLag(r, "over many changes", sweep(windowTextPoint(r, "f000.txt"), windowTextPoint(r, "f080.txt"), 200), targets, 30)
}
