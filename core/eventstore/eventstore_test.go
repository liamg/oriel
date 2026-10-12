package eventstore

import (
	"testing"

	"github.com/liamg/oriel/core"
)

func TestApplyAndSubscribe(t *testing.T) {
	s := New()
	var all, s2 []core.Event
	s.Subscribe(core.Filter{}, func(ev core.Event) { all = append(all, ev) })
	cancel := s.Subscribe(core.Filter{Session: "s2"}, func(ev core.Event) { s2 = append(s2, ev) })

	evs := []core.Event{
		core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "s2", Kind: core.KindPlan, Status: core.StatusQuiet},
		core.SessionStatusChanged{ID: "s2", Status: core.StatusWorking},
	}
	for _, ev := range evs {
		if err := s.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(all) != 3 || len(s2) != 2 {
		t.Errorf("all saw %d events, s2 saw %d; want 3 and 2", len(all), len(s2))
	}
	if se, ok := s.Session("s2"); !ok || se.Status() != core.StatusWorking || se.Kind() != core.KindPlan {
		t.Errorf("s2 = %v, %v", se, ok)
	}
	if n := len(s.Sessions()); n != 2 {
		t.Errorf("%d sessions, want 2", n)
	}

	cancel()
	s.Apply(core.SessionStatusChanged{ID: "s2", Status: core.StatusQuiet})
	if len(s2) != 2 {
		t.Error("a cancelled subscription still received events")
	}
}

func TestApplyRejectsUnknownSession(t *testing.T) {
	s := New()
	called := false
	s.Subscribe(core.Filter{}, func(core.Event) { called = true })
	if err := s.Apply(core.SessionStatusChanged{ID: "nope", Status: core.StatusWorking}); err == nil {
		t.Error("changing an unknown session's status succeeded")
	}
	if called {
		t.Error("a rejected event reached subscribers")
	}
}
