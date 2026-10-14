package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// waitAfter waits for a frame drawn after the first `after` frames that satisfies ok.
func (r *running) waitAfter(what string, after int, ok func(string) bool) string {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n := 0
	f, err := r.win.WaitFrame(ctx, func(f headless.Frame) bool { n++; return n > after && ok(f.Text()) })
	if err != nil {
		r.t.Fatalf("no frame with %s after frame %d; last frame:\n%s", what, after, f.Text())
	}
	return f.Text()
}

// cursorIs is a frame whose status line shows the cursor at line:col.
func cursorIs(pos string) func(string) bool {
	return func(s string) bool { return strings.Contains("\n"+s+"\n", "\n"+pos+"\n") }
}

// gg goes to the top at once, as in vim: the first g is a prefix of IDE maps (gd, grr…) and of the engine's own,
// and the second must reach the engine with it, however long after it comes.
func TestGGGoesToTheTop(t *testing.T) {
	var body strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	r := runEditor(t, map[string]string{"a.txt": body.String()}, []string{"a.txt"})
	r.waitText("line 1")
	for _, pause := range []time.Duration{0, 80 * time.Millisecond, 450 * time.Millisecond, 1200 * time.Millisecond} {
		r.typeKeys("G")
		r.waitAfter("the bottom", len(r.win.Frames()), cursorIs("50:1"))
		mark := len(r.win.Frames())
		r.typeKeys("g")
		time.Sleep(pause)
		r.typeKeys("g")
		r.waitAfter(fmt.Sprintf("the top, with %v between the g's", pause), mark, cursorIs("1:1"))
	}
}

// A command that holds the loop up is logged by name, so a stall says what caused it.
func TestSlowLoopIsLogged(t *testing.T) {
	logs := NewLogs(LogOpts{})
	r := startAppWith(t, t.TempDir(), "", func(h ext.Host) {
		h.Command(registry.Command{ID: "test.slow", Run: func(registry.Ctx, registry.Args) error { time.Sleep(80 * time.Millisecond); return nil }})
		h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-y>", Command: "test.slow"})
	}, func(c *Config) { c.Logs = logs })
	r.waitText("Open Directory")
	r.typeKeys("<D-y>")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range logs.Ring.Tail(50) {
			if rec.Msg == "the window was held up" && strings.Contains(rec.Text(), "test.slow") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no log of the slow command: %v", logs.Ring.Tail(10))
}

// A run of waiting pointer moves is handled as its last, and what follows it isn't lost.
func TestLastMove(t *testing.T) {
	move := func(x float32) render.PointerEvent {
		return render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: x}}
	}
	press := render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 3}, Buttons: render.ButtonPrimary}
	events := make(chan render.Event, 4)
	events <- move(1)
	events <- move(2)
	events <- press
	events <- move(4)
	ev, held := lastMove(move(0), events)
	if ev != render.Event(move(2)) || held != render.Event(press) {
		t.Fatalf("lastMove = %v, %v; want the move to 2, then the press", ev, held)
	}
	if ev, held := lastMove(press, events); ev != render.Event(press) || held != nil || len(events) != 1 {
		t.Errorf("a press read on: %v, %v, %d left", ev, held, len(events))
	}
}
