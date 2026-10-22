package gio

import (
	"sync"
	"testing"
	"time"

	"gioui.org/app"

	"github.com/liamg/oriel/render"
)

// The OS's preference is read at once when asked (the window came back to the front), not only on the slow poll.
func TestAppearanceIsReadAgainWhenAsked(t *testing.T) {
	var mu sync.Mutex
	dark, reads := false, 0
	read := func() (bool, bool) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return dark, true
	}
	got := make(chan bool, 4)
	stop, check := watchAppearance(read, time.Hour, func(d bool) { got <- d })
	defer stop()
	if d := <-got; d {
		t.Fatal("first reading is dark, want light")
	}
	mu.Lock()
	dark = true
	mu.Unlock()
	check()
	select {
	case d := <-got:
		if !d {
			t.Fatal("read light after the change, want dark")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("asking again didn't read the preference")
	}
	check()
	time.Sleep(20 * time.Millisecond)
	select {
	case d := <-got:
		t.Fatalf("sent %v again with no change", d)
	default:
	}
}

// The window's focus is an event when it changes, not on every config.
func TestFocusChangesAreEvents(t *testing.T) {
	focused := true
	var out []render.Event
	for _, f := range []bool{true, false, false, true} {
		if ev, ok := focusChange(app.ConfigEvent{Config: app.Config{Focused: f}}, &focused); ok {
			out = append(out, ev)
		}
	}
	want := []render.Event{render.FocusEvent{Focused: false}, render.FocusEvent{Focused: true}}
	if len(out) != len(want) || out[0] != want[0] || out[1] != want[1] {
		t.Fatalf("events %v, want %v", out, want)
	}
}
