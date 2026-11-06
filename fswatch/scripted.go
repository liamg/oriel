package fswatch

import "sync"

// Script is a Watcher's events, as a test says they happen: nothing the OS sees reaches it.
type Script struct{ w *Watcher }

// NewScripted is a Watcher whose events are only what its Script sends, at once: watching a path asks the OS for
// nothing, and changes on disk go unseen.
func NewScripted() (*Watcher, *Script) {
	w := newWatcher(&scripted{done: make(chan struct{})})
	return w, &Script{w: w}
}

// Change sends an event for each path, as though each had just changed and settled.
func (s *Script) Change(paths ...string) {
	for _, p := range paths {
		select {
		case s.w.out <- Event{Path: p}:
		case <-s.w.done:
			return
		}
	}
}

// scripted is a backend that watches nothing.
type scripted struct {
	once sync.Once
	done chan struct{}
}

func (*scripted) watchDir(string) error  { return nil }
func (*scripted) watchFile(string) error { return nil }
func (*scripted) unwatch(string)         {}
func (*scripted) unwatchUnder(string)    {}
func (*scripted) watched() []string      { return nil }
func (b *scripted) run(func([]raw))      { <-b.done }
func (b *scripted) close() error {
	b.once.Do(func() { close(b.done) })
	return nil
}
