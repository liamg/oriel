package app_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
)

// A session behind the one in front streaming its agent's reply doesn't build the window for every token: what
// shows of it keeps up a few times a second.
func TestASessionBehindStreamsAtAFewFramesASecond(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir()})
	front := core.SessionState{ID: "front", Name: "front", Kind: core.KindWork}
	bg := core.SessionState{ID: "bg", Name: "behind", Kind: core.KindWork}
	for _, ev := range []core.Event{core.SessionSynced{Session: front}, core.SessionSynced{Session: bg}, core.SessionFocused{ID: front.ID}} {
		if err := r.Store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	r.WaitText("front")
	time.Sleep(500 * time.Millisecond) // what opening it set going settles
	// What the window draws anyway in a while, with nothing happening: the bound is on what the events add to it.
	span := 100*10*time.Millisecond + 2*behindRedrawForTest
	idle := len(r.Win.Frames())
	time.Sleep(span)
	idle = len(r.Win.Frames()) - idle
	before := len(r.Win.Frames())
	start := time.Now()
	for i := range 100 {
		item := core.Item{ID: "m", Kind: core.ItemAssistant, Text: fmt.Sprint("token ", i), Partial: true}
		if err := r.Store.Apply(core.TranscriptAppended{Session: bg.ID, Items: []core.Item{item}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	took := time.Since(start)
	time.Sleep(2 * behindRedrawForTest)
	n := len(r.Win.Frames()) - before
	// A frame asked for can draw a second as what it built settles: two for each of the few a second still leaves
	// the ~90 a frame for every event would draw far behind.
	if most := idle + 2*int(took/behindRedrawForTest) + 2; n > most {
		t.Errorf("100 events of a session behind drew %d frames in %v (%d drawn idle in as long), want at most %d", n,
			took.Round(time.Millisecond), idle, most)
	}
}

const behindRedrawForTest = 150 * time.Millisecond
