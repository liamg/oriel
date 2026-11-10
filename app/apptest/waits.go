package apptest

import (
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// WaitExit waits for the app to exit on its own, as quitting does, failing the test if it exited with an error.
func (w *Window) WaitExit() {
	w.T.Helper()
	select {
	case err := <-w.done:
		w.once.Do(func() {})
		if err != nil {
			w.T.Fatalf("the app exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		w.T.Fatal("Oriel didn't quit")
	}
}

// WaitSearch frames until project search has finished and the frame has want, and answers the frame's text.
func (s *Scene) WaitSearch(want string) string {
	s.T.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		PluginOf[*editor.Search](s, "search").Wait()
		s.Frame()
		text := s.Text()
		if strings.Contains(text, want) {
			return text
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("no %q:\n%s", want, text)
		}
	}
}

// Idle waits until the app has nothing under way or due, over two frames running: no contributor busy
// (registry.Busy), no read a frame asked for (kit.Reads) under way, and no watched source (registry.Watches)
// reading, or due a read for a subscriber that wants one now. Each look waits for the frame built after it (the
// look asks for one), so what that frame starts is seen by the next. It's what the app is when it's left alone: not
// a while, the work done. It fails the test, naming what's still at work, if that never comes. The window must be
// showing: a hidden one builds nothing.
func (w *Window) Idle() {
	w.T.Helper()
	deadline := time.After(Timeout)
	for calm := 0; calm < 2; {
		busy, built := w.look()
		if len(busy) > 0 {
			calm = 0
		} else {
			calm++
		}
		select {
		case <-built:
		case <-deadline:
			w.T.Fatalf("the app never went idle: still at work: %s", strings.Join(append(busy, "the frame after a look"), ", "))
		}
	}
}

// look names what the app has under way or due, looked at on the loop, and answers a channel closed once the next
// frame is built.
func (w *Window) look() (busy []string, built <-chan struct{}) {
	done := make(chan struct{})
	w.Do(func() error {
		busy = append(registry.BusyNow(w.Host), dueSources(w.Host, w.Store.State())...)
		var once sync.Once
		var cancel func()
		cancel = w.Host.Framed(func() { once.Do(func() { cancel(); close(done) }) })
		return nil
	})
	return busy, done
}

// dueSources are the keys of the sources the app's and each session's managers are reading, or would read now.
func dueSources(r registry.Registry, st core.State) []string {
	ws, ok := registry.WatchesOf(r)
	if !ok {
		return nil
	}
	ms := []*watch.Manager{ws.App()}
	for _, se := range st.Sessions {
		ms = append(ms, ws.For(se.ID))
	}
	var out []string
	for _, m := range ms {
		if m == nil {
			continue
		}
		for _, s := range m.Stats() {
			active := s.State == "watching" || s.State == "polling"
			if s.State == "reading" || active && (s.Stale || s.LastRead.IsZero()) {
				out = append(out, m.Name()+": "+s.Key)
			}
		}
	}
	return out
}
