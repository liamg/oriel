package fswatch

import "sync"

// Script is a Watcher's events, as a test says they happen: nothing the OS sees reaches it.
type Script struct{ w *Watcher }

// NewScripted is a Watcher whose events are only what its Script sends: watching a path asks the OS for nothing,
// and changes on disk go unseen.
func NewScripted() (*Watcher, *Script) {
	w := newWatcher(&scripted{done: make(chan struct{})})
	return w, &Script{w: w}
}

// Change sends an event for each path, as though each had just changed and settled, and returns once whoever reads
// Events has said each is Done, or the watcher has closed.
func (s *Script) Change(paths ...string) {
	for _, p := range paths {
		done := make(chan struct{})
		if !s.send(Event{Path: p, done: done}) {
			return
		}
		select {
		case <-done:
		case <-s.w.done:
			return
		}
	}
}

// send hands ev to Events, false if the watcher closed first.
func (s *Script) send(ev Event) bool {
	s.w.sends.Lock()
	defer s.w.sends.Unlock()
	select {
	case <-s.w.done:
		return false
	default:
	}
	select {
	case s.w.out <- ev:
		return true
	case <-s.w.done:
		return false
	}
}

// Scripts make scripted Watchers, for something that starts several (an app's managers), and send a change to every
// one of them still open.
type Scripts struct {
	mu      sync.Mutex
	scripts []*Script
}

// New is a scripted Watcher of s's; its signature is fswatch.New's.
func (s *Scripts) New() (*Watcher, error) {
	w, sc := NewScripted()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, sc)
	return w, nil
}

// Change sends the paths to every open Watcher s has made (Script.Change), returning once each has taken them in.
func (s *Scripts) Change(paths ...string) {
	s.mu.Lock()
	var open []*Script
	for _, sc := range s.scripts {
		select {
		case <-sc.w.done:
		default:
			open = append(open, sc)
		}
	}
	s.scripts = open
	s.mu.Unlock()
	for _, sc := range open {
		sc.Change(paths...)
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
