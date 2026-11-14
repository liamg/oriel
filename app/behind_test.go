package app_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/internal/clock/clocktest"
)

// redraws is a clock the test moves that counts the functions it has run: the window's paced redraws.
type redraws struct {
	*clocktest.Fake
	ran atomic.Int32
}

func (c *redraws) AfterFunc(d time.Duration, f func()) clock.Timer {
	return c.Fake.AfterFunc(d, func() { c.ran.Add(1); f() })
}

// A session behind the one in front streaming its agent's reply doesn't build the window for every token: what
// shows of it keeps up a few times a second.
func TestASessionBehindStreamsAtAFewFramesASecond(t *testing.T) {
	c := &redraws{Fake: clocktest.New(time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: func(cfg *apptest.Config) { cfg.Clock = c }})
	front := core.SessionState{ID: "front", Name: "front", Kind: core.KindWork}
	bg := core.SessionState{ID: "bg", Name: "behind", Kind: core.KindWork}
	apply := func(ev core.Event) {
		t.Helper()
		if err := r.Store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: front}, core.SessionSynced{Session: bg}, core.SessionFocused{ID: front.ID}} {
		apply(ev)
	}
	r.WaitText("front")
	c.Advance(behindRedrawForTest) // what opening it asked for
	token := func(id core.SessionID, i int) core.Event {
		return core.TranscriptAppended{Session: id, Items: []core.Item{{ID: "m", Kind: core.ItemAssistant, Text: fmt.Sprint("token ", i), Partial: true}}}
	}

	for i := range 10 {
		apply(token(front.ID, i))
	}
	if n := c.Waiting(); n != 0 {
		t.Errorf("the session in front's events waited %d redraws, want them drawn at once", n)
	}

	before := c.ran.Load()
	for i := range 100 {
		apply(token(bg.ID, i))
	}
	if n, ran := c.Waiting(), c.ran.Load()-before; n != 1 || ran != 0 {
		t.Errorf("100 events at once of a session behind: %d redraws waiting and %d run, want 1 waiting", n, ran)
	}
	c.Advance(behindRedrawForTest)
	if ran := c.ran.Load() - before; ran != 1 {
		t.Errorf("100 events at once of a session behind drew %d times, want 1", ran)
	}

	// 100 events 10ms apart are a second of them: a redraw at the first, and one for the first after each.
	before = c.ran.Load()
	for i := range 100 {
		apply(token(bg.ID, i))
		c.Advance(10 * time.Millisecond)
	}
	c.Advance(behindRedrawForTest)
	if ran, want := c.ran.Load()-before, int32((time.Second+behindRedrawForTest-1)/behindRedrawForTest); ran != want {
		t.Errorf("a second of events of a session behind, 10ms apart, drew %d times, want %d", ran, want)
	}
}

const behindRedrawForTest = 150 * time.Millisecond
