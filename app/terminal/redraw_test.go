package terminal

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// A change asked for inside the interval is drawn when it ends, never dropped; changes in between share that frame.
func TestPacedRedrawKeepsTheTrailingFrame(t *testing.T) {
	var n atomic.Int32
	c := clocktest.New(time.Unix(0, 0))
	p := &pacedRedraw{Interval: 50 * time.Millisecond, Redraw: func() { n.Add(1) }, Clock: c}
	p.Ask()
	if n.Load() != 1 {
		t.Fatalf("the first ask redrew %d times, want at once", n.Load())
	}
	p.Ask()
	p.Ask()
	if n.Load() != 1 {
		t.Fatalf("asks inside the interval redrew at once")
	}
	c.Advance(50 * time.Millisecond)
	if got := n.Load(); got != 2 {
		t.Fatalf("redrew %d times, want the trailing one: 2", got)
	}
	p.Stop()
}
