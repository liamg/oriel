package kit

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/leaktest"
	"github.com/liamg/oriel/watch"
)

// A subscription Subs pauses keeps its reading and reads nothing; held again, it reads what went stale, and Held
// hands it back typed.
func TestSubsPauseKeepsTheReading(t *testing.T) {
	leaktest.Check(t)
	clk := clocktest.New(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	m := watch.New(watch.Options{Name: "test", Clock: clk})
	t.Cleanup(m.Close)
	var subs Subs
	t.Cleanup(subs.Close)
	var reads atomic.Int32
	src := watch.Source[int]{Key: "n", Poll: true, Read: func(watch.Reader) (int, error) { return int(reads.Add(1)), nil }}
	need := watch.Need{MaxAge: time.Second}
	hold := func() {
		subs.Hold("n", m, need, func() Closer { return watch.Subscribe(m, src, need, "test") })
	}
	step := func(d time.Duration) {
		m.Idle()
		clk.Advance(d)
		m.Idle()
	}
	value := func() int {
		t.Helper()
		s, ok := Held[int](&subs, "n")
		if !ok {
			t.Fatal("n isn't held")
		}
		v, _, _ := s.Get()
		return v
	}

	hold()
	step(0)
	if v := value(); v != 1 {
		t.Fatalf("first reading = %d", v)
	}
	if _, ok := Held[string](&subs, "n"); ok {
		t.Error("Held handed back a subscription of another type")
	}
	subs.PauseOnly(nil)
	step(5 * time.Second)
	if n := reads.Load(); n != 1 {
		t.Errorf("paused, it read %d times", n)
	}
	if v := value(); v != 1 {
		t.Errorf("paused, the reading = %d, want the one kept", v)
	}
	var seen []string
	EachHeld(&subs, func(key string, s watch.Sub[int]) { seen = append(seen, key) })
	if len(seen) != 1 || seen[0] != "n" {
		t.Errorf("EachHeld saw %v", seen)
	}
	hold()
	step(0)
	if v := value(); v != 2 {
		t.Errorf("held again, the reading = %d, want a fresh one", v)
	}
}
