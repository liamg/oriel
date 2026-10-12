package dev

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/liamg/oriel/render"
	giorender "github.com/liamg/oriel/render/gio"
	"github.com/liamg/oriel/render/scenes"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

// Window opens a scene in a window. With auto it runs the scripted measurements, prints them and
// exits. It never returns: it ends the process when the window closes.
func Window(name string, auto bool, phases string) error {
	s, ok := scenes.ByName(name)
	if !ok {
		return fmt.Errorf("no scene %q", name)
	}
	if auto {
		if err := pickPhases(phases); err != nil {
			return err
		}
		fmt.Println("Keep the window visible and in front, and don't touch the mouse or keyboard until it prints.")
	}
	go func() {
		w := new(app.Window)
		width, height := unit.Dp(scenes.IDEWidth), unit.Dp(scenes.IDEHeight)
		if name == "type" {
			width, height = 900, 160
		}
		w.Option(app.Title("oriel dev"), app.Size(width, height))
		w.Perform(system.ActionRaise)
		if auto {
			go kick(w)
		}
		if err := loop(w, s, auto); err != nil {
			fmt.Fprintln(os.Stderr, "oriel dev:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	giorender.Main()
	return nil
}

// phase is one step of the -auto script.
type phase struct {
	scene      string
	frames     int           // continuous frames; 0 means wait for dur without invalidating except at 4 Hz
	dur        time.Duration // for HUD and idle phases
	invalidate time.Duration // HUD redraw interval
}

var script = []phase{
	{scene: "ide", frames: 120},
	{scene: "scroll", frames: 1200},
	{scene: "glow", frames: 600},
	{scene: "text", frames: 60},
	{scene: "hud", dur: 10 * time.Second, invalidate: 250 * time.Millisecond},
	{scene: "ide", dur: 5 * time.Second},
}

// pickPhases keeps the script's phases for the named scenes, in order.
func pickPhases(names string) error {
	if names == "" {
		return nil
	}
	var out []phase
	for _, n := range strings.Split(names, ",") {
		found := false
		for _, p := range script {
			if p.scene == n {
				out, found = append(out, p), true
			}
		}
		if !found {
			return fmt.Errorf("-phases: no phase for scene %q", n)
		}
	}
	script = out
	return nil
}

// stallAt is the frame interval treated as a stall (window hidden, occluded, or the display asleep)
// rather than a slow frame.
const stallAt = 100 * time.Millisecond

type phaseStats struct {
	build, frame, interval Durations
	stalls                 Durations
	cpu                    time.Duration
	wall                   time.Duration
	gcs                    uint32
}

// nextFrame is when -auto expects its next frame (unix nanos), for kick.
var nextFrame atomic.Int64

// kicks counts frames kick had to request; kickedAt is when the latest kicked frame was due
// (unix nanos), cleared when it arrives.
var (
	kicks    atomic.Int64
	kickedAt atomic.Int64
	kickLate Durations
)

// kick invalidates the window when an expected frame is 50 ms late. On macOS the
// display link can stall until the window gets an input event.
func kick(w *app.Window) {
	const late = 50 * time.Millisecond
	nextFrame.Store(time.Now().UnixNano())
	for {
		if wait := time.Until(time.Unix(0, nextFrame.Load()).Add(late)); wait > 0 {
			time.Sleep(wait)
			continue
		}
		kicks.Add(1)
		kickedAt.CompareAndSwap(0, nextFrame.Load())
		w.Invalidate()
		time.Sleep(late)
	}
}

func loop(w *app.Window, s scenes.Scene, auto bool) error {
	ts, err := typeset.New(w.Invalidate)
	if err != nil {
		return err
	}
	var ops op.Ops
	th, err := scenes.Builtin(theme.Dark)
	if err != nil {
		return err
	}
	c := scenes.NewCanvas(ts, th)
	f := giorender.NewFrame()
	st := scenes.NewState()
	start := time.Now()
	var firstFrame time.Duration
	tag := new(int)

	// -auto bookkeeping.
	pi, pframes := 0, 0
	var pstart time.Time
	var pcpu time.Duration
	var lastFrame time.Time
	stats := make([]phaseStats, len(script))
	var keyLat Durations
	var pendingKey time.Time
	var pgc uint32
	var firstRSS float64
	var size string
	if auto {
		s, _ = scenes.ByName(script[0].scene)
	}

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			printKeys(auto, keyLat)
			return e.Err
		case app.FrameEvent:
			t0 := time.Now()
			gtx := app.NewContext(&ops, e)
			event.Op(gtx.Ops, tag)
			gtx.Execute(key.FocusCmd{Tag: tag})
			for {
				ev, ok := gtx.Event(key.FocusFilter{Target: tag}, key.Filter{Focus: tag, Name: ""}, key.Filter{Focus: tag, Name: "J"}, key.Filter{Focus: tag, Name: "K"}, key.Filter{Focus: tag, Name: key.NameDeleteBackward})
				if !ok {
					break
				}
				switch ev := ev.(type) {
				case key.Event:
					if ev.State != key.Press {
						continue
					}
					switch ev.Name {
					case "J":
						st.Line++
					case "K":
						st.Line--
					case key.NameEscape:
						printKeys(auto, keyLat)
						return nil
					case key.NameDeleteBackward:
						if len(st.Typed) > 0 {
							st.Typed = st.Typed[:len(st.Typed)-1]
						}
					}
					pendingKey = t0
				case key.EditEvent:
					st.Typed = replaceRunes(st.Typed, ev.Range, ev.Text)
					pendingKey = t0
				}
			}
			// Keep the platform's text-input state in step with ours, as a real editor must.
			n := len(st.Typed)
			gtx.Execute(key.SnippetCmd{Tag: tag, Snippet: key.Snippet{Range: key.Range{Start: 0, End: n}, Text: string(st.Typed)}})
			gtx.Execute(key.SelectionCmd{Tag: tag, Range: key.Range{Start: n, End: n}})
			st.T = time.Since(start).Seconds()
			if at := kickedAt.Swap(0); at != 0 {
				kickLate = append(kickLate, t0.Sub(time.Unix(0, at)))
			}
			// The next frame is requested before Frame: Gio reads InvalidateCmd while it processes
			// this frame, and one executed after Frame returns can be missed (see P0-4-gio.md).
			var ends bool
			if auto {
				p := script[pi]
				ends = p.frames > 0 && pframes+1 > p.frames || p.frames == 0 && pframes > 0 && t0.Sub(pstart) >= p.dur
				next := t0
				switch {
				case ends || p.frames > 0:
				case p.invalidate > 0:
					next = t0.Add(p.invalidate)
				case pframes == 0:
					next = t0.Add(p.dur)
				default:
					next = pstart.Add(p.dur)
				}
				if !ends || pi+1 < len(script) {
					gtx.Execute(op.InvalidateCmd{At: next})
					nextFrame.Store(next.UnixNano())
				}
			} else if s.Name() == "glow" || s.Name() == "hud" {
				gtx.Execute(op.InvalidateCmd{})
			}
			f.Begin(gtx.Ops, gtx.Metric.PxPerDp)
			var l render.List
			c.Begin(l.Record(gtx.Metric.PxPerDp))
			s.Draw(c, st)
			l.Replay(f)
			f.End()
			t1 := time.Now()
			e.Frame(gtx.Ops)
			t2 := time.Now()
			if firstFrame == 0 {
				firstFrame = t2.Sub(start)
				firstRSS = currentRSS()
				px := e.Size.X * e.Size.Y * 4
				size = fmt.Sprintf("%d×%d px at %.3g×; one drawable is %.0f MB", e.Size.X, e.Size.Y, gtx.Metric.PxPerDp, float64(px)/(1<<20))
			}
			if !pendingKey.IsZero() {
				keyLat = append(keyLat, t2.Sub(pendingKey))
				pendingKey = time.Time{}
			}
			if !auto {
				continue
			}

			// Scripted measurement.
			p := script[pi]
			if pframes == 0 {
				pstart, pcpu = t0, cpuTime()
				if p.frames == 0 {
					// Timed phases start after the scene's first frame and a GC, so they measure
					// the steady state rather than the switch.
					runtime.GC()
					pstart, pcpu = time.Now(), cpuTime()
				}
				pgc = numGC()
			} else {
				ps := &stats[pi]
				ps.build = append(ps.build, t1.Sub(t0))
				ps.frame = append(ps.frame, t2.Sub(t1))
				if d := t0.Sub(lastFrame); d >= stallAt && p.frames > 0 {
					ps.stalls = append(ps.stalls, d)
				} else {
					ps.interval = append(ps.interval, d)
				}
			}
			lastFrame = t0
			pframes++
			st.Line++
			if ends {
				stats[pi].cpu = cpuTime() - pcpu
				stats[pi].wall = time.Since(pstart)
				stats[pi].gcs = numGC() - pgc
				pi, pframes = pi+1, 0
				if pi == len(script) {
					report(firstFrame, firstRSS, size, stats, keyLat)
					return nil
				}
				s, _ = scenes.ByName(script[pi].scene)
				st = scenes.NewState()
			}
		}
	}
}

// replaceRunes replaces the runes in r (clamped to t) with text.
func replaceRunes(t []rune, r key.Range, text string) []rune {
	start, end := min(r.Start, r.End), max(r.Start, r.End)
	start, end = max(0, min(start, len(t))), max(0, min(end, len(t)))
	out := append([]rune{}, t[:start]...)
	out = append(out, []rune(text)...)
	return append(out, t[end:]...)
}

// printKeys reports key latency when a manual (non -auto) run ends.
func printKeys(auto bool, keys Durations) {
	if !auto && len(keys) > 0 {
		fmt.Printf("key event → frame presented (in-app part only): %s\n", keys)
	}
}

func numGC() uint32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.NumGC
}

func mean(ds Durations) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var t time.Duration
	for _, d := range ds {
		t += d
	}
	return t / time.Duration(len(ds))
}

func report(first time.Duration, firstRSS float64, size string, stats []phaseStats, keys Durations) {
	fmt.Printf("window %s\n", size)
	fmt.Printf("startup: first frame presented %s after start; RSS then %.0f MB\n", first.Round(time.Millisecond), firstRSS)
	if n := kicks.Load(); n > 0 {
		fmt.Printf("kicked %d times for %d late frames, which then came %s after they were due\n", n, len(kickLate), kickLate)
	} else {
		fmt.Println("no late frames to kick")
	}
	stalled := false
	for i, p := range script {
		ps := stats[i]
		fmt.Printf("%-7s", p.scene)
		if p.frames > 0 {
			fmt.Printf(" build ops %s\n        frame()   %s\n        interval  %s\n", ps.build, ps.frame, ps.interval)
			if len(ps.stalls) > 0 {
				stalled = true
				fmt.Printf("        stalls (window hidden?) %s, not in interval\n", ps.stalls)
			}
			continue
		}
		cpu := 100 * ps.cpu.Seconds() / ps.wall.Seconds()
		kind := map[bool]string{true: "redrawing at 4 Hz", false: "idle"}[p.invalidate > 0]
		fmt.Printf(" %s (after its first frame) over %s: CPU %.2f%% of one core (%d frames, %d GCs)\n", kind, ps.wall.Round(time.Millisecond), cpu, len(ps.build), ps.gcs)
		if n := len(ps.build); n > 0 {
			per := ps.cpu / time.Duration(n)
			b, f := mean(ps.build), mean(ps.frame)
			fmt.Printf("        per frame: CPU %.2f ms = build ops %.2f + frame() %.2f + the rest %.2f (event loop, present, GC, other threads)\n",
				msf(per), msf(b), msf(f), msf(per-b-f))
		}
	}
	if len(keys) > 0 {
		fmt.Printf("key event → frame presented (in-app part only): %s\n", keys)
	}
	fmt.Printf("peak RSS: %.0f MB\n", peakRSS())
	memReport(os.Stdout)
	if stalled {
		fmt.Println("WARNING: frames stalled for 100 ms or more, so the window was probably hidden, covered or the display slept.")
		fmt.Println("Run again with the window visible and in front, without touching anything.")
	}
}

func msf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
