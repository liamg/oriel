package apptest

import (
	"strings"
	"time"

	"github.com/liamg/oriel/registry"
)

// Quiet runs frames and what the loop is handed until the app has settled: no contributor's work under way
// (registry.Busy), nothing handed to the loop and no redraw asked for, two frames running. It's what a person
// waits for before they click or read: not a while, the work done. It fails the test, naming what's still busy,
// if that never comes.
func (s *Scene) Quiet() {
	s.T.Helper()
	if s.midway {
		return
	}
	deadline := time.Now().Add(Timeout)
	calm := 0
	for {
		s.RunQueued()
		s.Frame()
		busy := registry.BusyNow(s.Registry())
		if len(busy) == 0 && !s.Queued() && !s.Poked() {
			if calm++; calm == 2 {
				return
			}
			continue
		}
		calm = 0
		if time.Now().After(deadline) {
			if s.Poked() {
				busy = append(busy, "a redraw asked for each frame")
			}
			s.T.Fatalf("the app never settled: still busy: %s", strings.Join(busy, ", "))
		}
		time.Sleep(time.Millisecond)
	}
}

// Midway runs fn with the app looked at as it is, without settling it first: a test of what shows while work is
// under way (a loading skeleton, a spinner), which settling would end.
func (s *Scene) Midway(fn func()) {
	was := s.midway
	s.midway = true
	defer func() { s.midway = was }()
	fn()
}
