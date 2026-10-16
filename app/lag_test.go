//go:build !race

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
func lagApp(t *testing.T) *running { return lagAppWith(t, nil) }

func lagAppWith(t *testing.T, change func(*Config)) *running {
	r := startAppWith(t, t.TempDir(), lagRepo(t), nil, change)
	r.do(func() error {
		return r.host.Run(context.Background(), "view.toggle", registry.Args{"id": ExplorerView})
	})
	r.do(func() error { return r.host.Run(context.Background(), "explorer.scope", registry.Args{"scope": "all"}) })
	r.waitText("pkg")
	r.clickText("pkg")
	r.waitText("f25.go")
	r.settle()
	return r
}

// settle waits until no frame has been drawn for a moment.
func (r *running) settle() {
	for n := -1; ; {
		time.Sleep(150 * time.Millisecond)
		if m := len(r.win.Frames()); m == n {
			return
		} else {
			n = m
		}
	}
}

// textPoint is the middle of the run reading text in the frame on screen.
func (r *running) textPoint(text string) render.Point {
	r.t.Helper()
	var at render.Point
	r.latest(`"`+text+`"`, func(f headless.Frame) bool {
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

// moves sends the pointer along path, one point every moveEvery, and says how far it fell behind: the loop not
// taking moves as fast as they come.
func (r *running) moves(path []render.Point, buttons render.Buttons) time.Duration {
	start := time.Now()
	var behind time.Duration
	for i, p := range path {
		due := start.Add(time.Duration(i) * moveEvery)
		if d := time.Until(due); d > 0 {
			time.Sleep(d)
		}
		r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p, Buttons: buttons})
		behind = max(behind, time.Since(due))
	}
	return behind
}

// probe jumps the pointer to p, and times it until a frame draws something else under p.
func (r *running) probe(p render.Point) time.Duration {
	r.t.Helper()
	fs := r.win.Frames()
	before, n := topFill(fs[len(fs)-1], p), len(fs)
	start := time.Now()
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	for time.Since(start) < 2*time.Second {
		fs := r.win.Frames()
		for _, f := range fs[n:] {
			if topFill(f, p) != before {
				return time.Since(start)
			}
		}
		n = len(fs)
		time.Sleep(200 * time.Microsecond)
	}
	r.t.Fatalf("nothing lit up under %v in 2s", p)
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

// lag sweeps noise for 200 ms then probes each target in turn, rounds times, and checks the run against the budgets.
func (r *running) lag(name string, noise []render.Point, targets []render.Point, rounds int) lagRun {
	r.t.Helper()
	var run lagRun
	for i := range rounds {
		run.behind = max(run.behind, r.moves(noise, 0))
		run.probes = append(run.probes, r.probe(targets[i%len(targets)]))
	}
	r.check(name, run)
	return run
}

func (r *running) check(name string, run lagRun) {
	r.t.Helper()
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
	r.t.Logf("%s: %d probes, p90 %v, worst %v, first third %v, last third %v; moves fell %v behind", name, len(run.probes),
		p90, worst, first, last, run.behind)
	if p90 > probeP90 || worst > probeMax {
		r.t.Errorf("%s: the frame showing what's under the pointer took %v (p90), %v at worst; the budget is %v, %v", name, p90, worst, probeP90, probeMax)
	}
	if last > 2*first+10*time.Millisecond {
		r.t.Errorf("%s: probes slowed down over the run: %v at the start, %v at the end", name, first, last)
	}
	if run.behind > sendBehind {
		r.t.Errorf("%s: the loop fell %v behind moves at 1 kHz", name, run.behind)
	}
}

// Sweeping the Explorer's rows, each row the pointer lands on lights up within budget, as quickly at the end of a
// long run as at its start.
func TestExplorerHoverLagBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	top, mid := r.textPoint("f00.go"), r.textPoint("f10.go")
	noise := sweep(top, mid, 200)
	var targets []render.Point
	for _, f := range []string{"f20.go", "f14.go", "f25.go", "f17.go", "f22.go"} {
		targets = append(targets, r.textPoint(f))
	}
	r.lag("explorer", noise, targets, 60)
}

// The same after drags over the Explorer: a drag that ends mustn't leave moves building frames they didn't before,
// or slow the ones that do.
func TestHoverLagAfterDragBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	row := r.textPoint("f00.go")
	along := func() int {
		r.moves([]render.Point{row}, 0)
		r.settle()
		n := len(r.win.Frames())
		r.moves(sweep(row, render.Point{X: row.X + 30, Y: row.Y}, 200), 0)
		r.settle()
		return len(r.win.Frames()) - n
	}
	clean := along()
	a, b := r.textPoint("f02.go"), r.textPoint("f12.go")
	for range 5 {
		r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: a, Buttons: render.ButtonPrimary})
		r.moves(sweep(a, b, 80), render.ButtonPrimary)
		r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: b, Buttons: render.ButtonPrimary})
	}
	r.win.Send(render.KeyEvent{Key: "Esc"})
	r.settle()
	if n := along(); n > clean+3 {
		t.Errorf("200 moves along one row built %d frames after drags, %d before", n, clean)
	}
	var targets []render.Point
	for _, f := range []string{"f20.go", "f14.go", "f25.go"} {
		targets = append(targets, r.textPoint(f))
	}
	r.lag("after drags", sweep(row, r.textPoint("f10.go"), 200), targets, 30)
}

// A context menu over the Explorer: its items light up within budget while the pointer keeps moving.
func TestMenuHoverLagBudget(t *testing.T) {
	perf.Timed(t)
	r := lagApp(t)
	at := r.textPoint("f05.go")
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonSecondary})
	r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonSecondary})
	r.waitText("Close")
	r.settle()
	first, second, third := r.textPoint("Move pane…"), r.textPoint("Close"), r.textPoint("? Help")
	r.lag("menu", sweep(first, render.Point{X: first.X + 60, Y: first.Y}, 200), []render.Point{second, third}, 40)
}

// A move that reaches the loop long after it happened is logged with how far behind it was, so lag on a machine
// tests can't reproduce says where it is.
func TestPointerFallingBehindIsLogged(t *testing.T) {
	logs := NewLogs(LogOpts{})
	r := lagAppWith(t, func(c *Config) { c.Logs = logs })
	warned := func() (string, bool) {
		for _, rec := range logs.Ring.Tail(50) {
			if rec.Msg == "the pointer fell behind" {
				return rec.Text(), true
			}
		}
		return "", false
	}
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: r.textPoint("f00.go"), At: time.Now()})
	r.settle()
	if got, ok := warned(); ok {
		t.Fatalf("a move on time was logged: %s", got)
	}
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: r.textPoint("f20.go"), At: time.Now().Add(-300 * time.Millisecond)})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := warned(); ok {
			if !strings.Contains(got, "drawn after") {
				t.Errorf("the log doesn't say how far behind: %s", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a move 300 ms late wasn't logged: %v", logs.Ring.Tail(10))
}

// A frame the window was slow to put on screen is logged with where the time went.
func TestSlowFrameIsLogged(t *testing.T) {
	logs := NewLogs(LogOpts{})
	r := startAppWith(t, t.TempDir(), t.TempDir(), nil, func(c *Config) { c.Logs = logs })
	r.win.Send(render.SlowFrameEvent{Waited: 900 * time.Millisecond, Took: 20 * time.Millisecond})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range logs.Ring.Tail(50) {
			if rec.Msg == "the window was slow to show a frame" {
				if !strings.Contains(rec.Text(), "900ms") {
					t.Errorf("the log doesn't say how long it waited: %s", rec.Text())
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no log of the slow frame: %v", logs.Ring.Tail(10))
}

// changedRepo is a repo with n files of 100 lines changed since its one commit, every fifth line.
func changedRepo(t *testing.T, n int) string {
	r := gittest.New(t)
	before, after := map[string]string{}, map[string]string{}
	for i := range n {
		var b, a strings.Builder
		for l := range 100 {
			fmt.Fprintf(&b, "line %d\n", l)
			if l%5 == 0 {
				fmt.Fprintf(&a, "line %d, changed\n", l)
			} else {
				fmt.Fprintf(&a, "line %d\n", l)
			}
		}
		p := fmt.Sprintf("pkg%d/f%03d.txt", i%20, i)
		before[p], after[p] = b.String(), a.String()
	}
	r.Write(before)
	r.Commit("Ada", "base")
	r.Write(after)
	return r.Dir
}

// explorerFrame is the average frame with the Explorer open on dir's Changed scope, once every diff is made.
func explorerFrame(t *testing.T, dir string, n int) time.Duration {
	s := newScene(t, "", dir, mockupSize)
	s.sh.git.wait()
	s.keyCommand(t, "<M-1>")
	ready := func() bool {
		l, ok := registry.LedgerOf(s.reg, dir)
		return ok && l.Ready
	}
	if !s.Until(30*time.Second, ready) {
		t.Fatalf("the ledger over %d changed files was never ready", n)
	}
	for range 10 {
		s.frame()
	}
	start := time.Now()
	for range 100 {
		s.frame()
	}
	return time.Since(start) / 100
}

// The Explorer's frame doesn't grow with the files a branch changes: the review ledger it shows is made when a diff
// lands or the status changes, not every frame.
func TestExplorerFrameWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	few, many := explorerFrame(t, changedRepo(t, 5), 5), explorerFrame(t, changedRepo(t, 400), 400)
	t.Logf("a frame over 5 changed files: %v; over 400: %v", few, many)
	if many > 2*few+500*time.Microsecond {
		t.Errorf("400 changed files make the Explorer's frame %v, against %v over 5", many, few)
	}
}

// With --trace, a stall writes the execution trace of the seconds before it, for go tool trace.
func TestSlowFrameWritesTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stall.trace")
	r := startAppWith(t, t.TempDir(), t.TempDir(), nil, func(c *Config) { c.Trace = path })
	r.win.Send(render.SlowFrameEvent{Waited: 900 * time.Millisecond})
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
