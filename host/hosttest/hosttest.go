// Package hosttest is a harness for testing a part of the session host alone: a hostcore.State over a store of its
// own, and an Env on a temporary home.
package hosttest

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/internal/isolate"
)

// State is a hostcore.State over a store of its own.
type State struct {
	Store *eventstore.Store
	edit  sync.Mutex
}

var _ hostcore.State = (*State)(nil)

// NewState is an empty state.
func NewState() *State { return &State{Store: eventstore.New()} }

func (s *State) Snapshot() core.State { return s.Store.State() }

func (s *State) Session(id core.SessionID) (core.SessionState, error) {
	se, ok := s.Store.State().Session(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %s", id)
	}
	return se.Snapshot(), nil
}

func (s *State) Mutate(id core.SessionID, fn func(se core.SessionState) error) error {
	s.edit.Lock()
	defer s.edit.Unlock()
	se, err := s.Session(id)
	if err != nil {
		return err
	}
	return fn(se)
}

func (s *State) Emit(ev core.Event) error { return s.Store.Apply(ev) }

// Env is an Env on a temporary home, with the real clock, a log that's thrown away and its goroutines counted in Work;
// it closes with the test.
func Env(t testing.TB) hostcore.Env {
	isolate.Test(t)
	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })
	return hostcore.Env{Home: t.TempDir(), Log: slog.New(slog.DiscardHandler), Closed: closed, Work: &hostcore.Jobs{}}
}
