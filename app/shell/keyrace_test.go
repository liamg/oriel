package shell_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/render"
)

// cursorIs is a frame whose status line shows the cursor at line:col.
func cursorIs(pos string) func(string) bool {
	return func(s string) bool { return strings.Contains("\n"+s+"\n", "\n"+pos+"\n") }
}

// gg goes to the top at once, as in vim: the first g is a prefix of IDE maps (gd, grr…) and of the engine's own,
// and the second must reach the engine with it, however long after it comes on the window's clock.
func TestGGGoesToTheTop(t *testing.T) {
	var body strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&body, "line %d\n", i)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.OpenFile("a.txt")
	s.Until("the file", func() bool { return strings.HasPrefix(s.Line(0), "line 1") })
	for _, pause := range []time.Duration{0, 80 * time.Millisecond, 450 * time.Millisecond, 1200 * time.Millisecond} {
		s.Type("G")
		s.Until("the bottom", func() bool { return s.EditorWindow().Cursor.Line == 49 })
		s.Type("g")
		s.Advance(pause)
		s.Type("g")
		s.Until(fmt.Sprintf("the top, with %v between the g's", pause), func() bool { return s.EditorWindow().Cursor.Line == 0 })
	}
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
	ev, held := shell.LastMove(move(0), events)
	if ev != render.Event(move(2)) || held != render.Event(press) {
		t.Fatalf("lastMove = %v, %v; want the move to 2, then the press", ev, held)
	}
	if ev, held := shell.LastMove(press, events); ev != render.Event(press) || held != nil || len(events) != 1 {
		t.Errorf("a press read on: %v, %v, %d left", ev, held, len(events))
	}
}
