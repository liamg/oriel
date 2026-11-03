package eventstore

import (
	"bytes"
	"strings"
	"testing"
	"time"

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

// An event that leaves the state as it was is neither published nor counted.
func TestApplyDropsNoOpEvents(t *testing.T) {
	s := New()
	var got []core.Event
	s.Subscribe(core.Filter{}, func(ev core.Event) { got = append(got, ev) })
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	working := core.AgentChanged{ID: "s1", Agent: core.AgentState{Provider: "fake", Running: true, Working: true}}
	for range 3 {
		if err := s.Apply(working); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 {
		t.Errorf("published %d events, want 2: the repeated AgentChanged changes nothing", len(got))
	}
	if seq := s.State().Seq; seq != 2 {
		t.Errorf("seq %d, want 2", seq)
	}
	s.Apply(noted{Session: "s1", Text: "hi"})
	if len(got) != 3 {
		t.Error("a Logged event that changes no state wasn't published")
	}
}

// One clock for event times: whatever zone an event was stamped in, the state and the log hold UTC.
// The caller's event is left as it was.
func TestEventTimesAreUTC(t *testing.T) {
	s := New()
	var log bytes.Buffer
	s.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	paris := time.FixedZone("CEST", 2*3600)
	at := time.Date(2026, 10, 14, 11, 0, 0, 0, paris)
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	th := core.Thread{ID: "t1", Created: at, Comments: []core.Comment{{ID: "c1", Body: "hi", Time: at}}}
	if err := s.Apply(core.ThreadUpdated{Session: "s1", Thread: th}); err != nil {
		t.Fatal(err)
	}
	s.Apply(core.Notified{Notice: core.Notice{ID: "n", Text: "x", Time: at}})
	se, _ := s.State().Session("s1")
	got, _ := se.Snapshot().Thread("t1")
	if got.Created.Location() != time.UTC || got.Comments[0].Time.Location() != time.UTC || !got.Created.Equal(at) {
		t.Errorf("thread times %v, %v; want %v in UTC", got.Created, got.Comments[0].Time, at.UTC())
	}
	if n := s.State().Notifications; len(n) != 1 || n[0].Time.Location() != time.UTC {
		t.Errorf("notice time %v, want UTC", n[0].Time)
	}
	if strings.Contains(log.String(), "+02:00") {
		t.Errorf("the log holds local times:\n%s", log.String())
	}
	if th.Created.Location() != paris || th.Comments[0].Time.Location() != paris {
		t.Error("the caller's event was changed")
	}
}
