//go:build spike

package main

import (
	"fmt"
	"os"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/liamg/oriel/cmd/oriel-spike/scene"
)

func runWindow(name string, auto bool) error {
	s, ok := scene.ByName(name)
	if !ok {
		return fmt.Errorf("no scene %q", name)
	}
	go func() {
		w := new(app.Window)
		w.Option(app.Title("oriel-spike"), app.Size(unit.Dp(scene.IDEWidth), unit.Dp(scene.IDEHeight)))
		if err := loop(w, s, auto); err != nil {
			fmt.Fprintln(os.Stderr, "oriel-spike:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
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

type phaseStats struct {
	build, frame, interval scene.Durations
	cpu                    time.Duration
	wall                   time.Duration
}

func loop(w *app.Window, s scene.Scene, auto bool) error {
	sh, err := scene.NewShaper(false)
	if err != nil {
		return err
	}
	var ops op.Ops
	st := scene.NewState()
	start := time.Now()
	var firstFrame time.Duration
	tag := new(int)

	// -auto bookkeeping.
	pi, pframes := 0, 0
	var pstart time.Time
	var pcpu time.Duration
	var lastFrame time.Time
	stats := make([]phaseStats, len(script))
	var keyLat scene.Durations
	var pendingKey time.Time
	if auto {
		s, _ = scene.ByName(script[0].scene)
	}

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
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
						return nil
					case key.NameDeleteBackward:
						if len(st.Typed) > 0 {
							st.Typed = st.Typed[:len(st.Typed)-1]
						}
					}
					pendingKey = t0
				case key.EditEvent:
					st.Typed = append(st.Typed, []rune(ev.Text)...)
					pendingKey = t0
				}
			}
			st.T = time.Since(start).Seconds()
			c := scene.NewCanvas(gtx.Ops, sh, gtx.Metric.PxPerDp)
			s.Draw(c, st)
			t1 := time.Now()
			e.Frame(gtx.Ops)
			t2 := time.Now()
			if firstFrame == 0 {
				firstFrame = t2.Sub(start)
			}
			if !pendingKey.IsZero() {
				keyLat = append(keyLat, t2.Sub(pendingKey))
				pendingKey = time.Time{}
			}
			if !auto {
				if s.Name() == "glow" || s.Name() == "hud" {
					gtx.Execute(op.InvalidateCmd{})
				}
				continue
			}

			// Scripted measurement.
			p := script[pi]
			if pframes == 0 {
				pstart, pcpu = t0, cpuTime()
			} else {
				ps := &stats[pi]
				ps.build = append(ps.build, t1.Sub(t0))
				ps.frame = append(ps.frame, t2.Sub(t1))
				ps.interval = append(ps.interval, t0.Sub(lastFrame))
			}
			lastFrame = t0
			pframes++
			st.Line++
			done := p.frames > 0 && pframes > p.frames || p.frames == 0 && time.Since(pstart) >= p.dur
			if done {
				stats[pi].cpu = cpuTime() - pcpu
				stats[pi].wall = time.Since(pstart)
				pi, pframes = pi+1, 0
				if pi == len(script) {
					report(firstFrame, stats, keyLat)
					return nil
				}
				s, _ = scene.ByName(script[pi].scene)
				st = scene.NewState()
				gtx.Execute(op.InvalidateCmd{})
				continue
			}
			switch {
			case p.frames > 0:
				gtx.Execute(op.InvalidateCmd{})
			case p.invalidate > 0:
				gtx.Execute(op.InvalidateCmd{At: t0.Add(p.invalidate)})
			default:
				gtx.Execute(op.InvalidateCmd{At: pstart.Add(p.dur)})
			}
		}
	}
}

func report(first time.Duration, stats []phaseStats, keys scene.Durations) {
	fmt.Printf("startup: first frame presented %s after start\n", first.Round(time.Millisecond))
	for i, p := range script {
		ps := stats[i]
		fmt.Printf("%-7s", p.scene)
		if p.frames > 0 {
			fmt.Printf(" build ops %s\n        frame()   %s\n        interval  %s\n", ps.build, ps.frame, ps.interval)
			continue
		}
		cpu := 100 * ps.cpu.Seconds() / ps.wall.Seconds()
		fmt.Printf(" %s over %s: CPU %.2f%% of one core (%d frames)\n", map[bool]string{true: "redrawing at 4 Hz", false: "idle"}[p.invalidate > 0], ps.wall.Round(time.Millisecond), cpu, len(ps.build))
	}
	if len(keys) > 0 {
		fmt.Printf("key event → frame presented (in-app part only): %s\n", keys)
	}
	fmt.Printf("peak RSS: %.0f MB\n", peakRSS())
}
