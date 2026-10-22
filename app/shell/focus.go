package shell

import "sync"

// WindowFocus is the payload of FocusGained and FocusLost: the window came to the front, or another app did.
type WindowFocus struct{ Focused bool }

// windowFocus is whether the window has the OS's focus, for work that slows or stops while it hasn't. It starts
// focused.
type windowFocus struct {
	mu      sync.Mutex
	off     bool
	changed chan struct{} // closed, and replaced, at each change
}

func newWindowFocus() *windowFocus { return &windowFocus{changed: make(chan struct{})} }

// Focused says whether the window has focus now.
func (f *windowFocus) Focused() bool {
	on, _ := f.now()
	return on
}

// now is whether the window has focus, and a channel closed when that changes.
func (f *windowFocus) now() (bool, <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.off, f.changed
}

// set reports whether on changed it.
func (f *windowFocus) set(on bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.off == !on {
		return false
	}
	f.off = !on
	close(f.changed)
	f.changed = make(chan struct{})
	return true
}
