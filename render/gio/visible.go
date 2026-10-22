package gio

import (
	"sync"

	"gioui.org/app"
	"gioui.org/io/event"
)

// visibility is whether the window can be seen: not minimised and, where the OS says, not wholly covered. tell
// hears each change; it's called with the lock held, so it mustn't call back.
type visibility struct {
	mu                  sync.Mutex
	minimised, occluded bool
	was                 bool // hidden, as last told
	tell                func(hidden bool)
}

func (v *visibility) config(e event.Event) {
	if c, ok := e.(app.ConfigEvent); ok {
		v.set(func() { v.minimised = c.Config.Mode == app.Minimized })
	}
}

func (v *visibility) occlude(on bool) { v.set(func() { v.occluded = on }) }

func (v *visibility) hidden() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.was
}

func (v *visibility) set(change func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	change()
	if h := v.minimised || v.occluded; h != v.was {
		v.was = h
		v.tell(h)
	}
}
