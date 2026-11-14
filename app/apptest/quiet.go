package apptest

import (
	"strings"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
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
		if !s.Queued() && !s.Poked() && s.stepClock() {
			continue
		}
		if time.Now().After(deadline) {
			if s.Poked() {
				busy = append(busy, "a redraw asked for each frame")
			}
			s.T.Fatalf("the app never settled: still busy: %s", strings.Join(busy, ", "))
		}
		time.Sleep(time.Millisecond)
	}
}

// Idle is Quiet, and more: no watched source (services.Watches) reading or due a read, no read a frame asked for
// (kit.Reads) under way and no git tree reading, two frames running, as Window.Idle. It's what a test that counts
// reads waits for before it counts, since a read's end can start another.
func (s *Scene) Idle() {
	s.T.Helper()
	if s.midway {
		return
	}
	deadline := time.Now().Add(Timeout)
	for calm := 0; calm < 2; {
		s.Quiet()
		busy := dueSources(s.Registry(), s.Store().State())
		if rd, ok := kit.ReadsOf(s.Registry()); ok && rd.Busy() {
			busy = append(busy, "the window's reads")
		}
		if kit.GitOf(s.Registry()).Busy() {
			busy = append(busy, "git")
		}
		if len(busy) == 0 {
			calm++
			continue
		}
		calm = 0
		if time.Now().After(deadline) {
			s.T.Fatalf("the app never went idle: still at work: %s", strings.Join(busy, ", "))
		}
		time.Sleep(time.Millisecond)
	}
}

// UntilEach is Until for a test of every frame on the way: it runs the work handed to the loop and draws a frame,
// calls check with the frame's number (0 is the first drawn), and stops once ok; it fails the test after Timeout.
func (s *Scene) UntilEach(what string, check func(frame int), ok func() bool) {
	s.T.Helper()
	deadline := time.Now().Add(Timeout)
	for n := 0; ; n++ {
		s.RunQueued()
		s.Frame()
		check(n)
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("waiting for %s:\n%s%s", what, s.Text(), rig.Stuck())
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
