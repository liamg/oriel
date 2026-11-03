package gio

import (
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// appearanceEvery is how often the OS's light or dark preference is read while nothing asks sooner: the window
// coming back to the front reads it at once.
const appearanceEvery = 30 * time.Second

// watchAppearance calls fn with read's preference now, then on every change, until stop; check reads it again now.
// A platform that can't tell never calls it.
func watchAppearance(read func() (dark, ok bool), every time.Duration, fn func(dark bool)) (stop, check func()) {
	done, kick := make(chan struct{}), make(chan struct{}, 1)
	go func() {
		last, known := read()
		if known {
			fn(last)
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			case <-kick:
			}
			if dark, ok := read(); ok && (!known || dark != last) {
				last, known = dark, true
				fn(dark)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// focusChange is the window's focus as an event, when e changes it from *focused.
func focusChange(e event.Event, focused *bool) (render.Event, bool) {
	c, ok := e.(app.ConfigEvent)
	if !ok || c.Config.Focused == *focused {
		return nil, false
	}
	*focused = c.Config.Focused
	return render.FocusEvent{Focused: *focused}, true
}
