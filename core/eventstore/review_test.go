package eventstore

import (
	"bytes"
	"testing"

	"github.com/liamg/oriel/core"
)

// Marks of reviewed hunks are session events: they replay from the log, add and take off per change, and
// leave older copies of the state alone.
func TestHunksReviewed(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	evs := []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing", Kind: core.KindWork}},
		core.HunksReviewed{Session: "pricing", Project: "pricing", File: "round.go", Hunks: []string{"b2", "a1"}, Reviewed: true},
		core.HunksReviewed{Session: "pricing", Project: "pricing", File: "invoice.go", Hunks: []string{"c3"}, Reviewed: true},
	}
	for _, ev := range evs {
		if err := a.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	before := a.State()
	if err := a.Apply(core.HunksReviewed{Session: "pricing", Project: "pricing", File: "round.go", Hunks: []string{"a1"}}); err != nil {
		t.Fatal(err)
	}
	se, _ := a.State().Session("pricing")
	st := se.Snapshot()
	if got := st.ReviewedHunks("pricing", "round.go"); len(got) != 1 || !got["b2"] {
		t.Errorf("round.go's marks: %v", got)
	}
	if got := st.ReviewedHunks("pricing", "invoice.go"); !got["c3"] {
		t.Errorf("invoice.go's marks: %v", got)
	}
	old, _ := before.Session("pricing")
	if got := old.Snapshot().ReviewedHunks("pricing", "round.go"); len(got) != 2 {
		t.Errorf("an older copy changed: %v", got)
	}
	a.Apply(core.HunksReviewed{Session: "pricing", Project: "pricing", File: "invoice.go", Hunks: []string{"c3"}})
	se, _ = a.State().Session("pricing")
	if n := len(se.Snapshot().Reviews); n != 1 {
		t.Errorf("a file with no marks left is still listed: %d reviews", n)
	}
	b := New()
	if err := core.Replay(bytes.NewReader(log.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed:\n%s\nwant:\n%s", sb, sa)
	}
	if err := a.Apply(core.HunksReviewed{Session: "pricing", File: "round.go"}); err == nil {
		t.Error("a mark with no hunks applied")
	}
}

// Every agent-written line is unread until you've seen it in the diff (scrolled past it, or marked its hunk
// reviewed): lines seen are session events too, a line read stays read, and a marked change reads all
// its lines. A commit made past the gate is kept in the session.
func TestLinesSeenAndGateOverrides(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing", Kind: core.KindWork}},
		core.LinesSeen{Session: "pricing", Project: "pricing", File: "round.go", Skimmed: []string{"c1.0", "c1.1"}},
		core.LinesSeen{Session: "pricing", Project: "pricing", File: "round.go", Read: []string{"c1.1"}, Skimmed: []string{"c1.2"}},
		core.LinesSeen{Session: "pricing", Project: "pricing", File: "round.go", Skimmed: []string{"c1.1"}},
		core.HunksReviewed{Session: "pricing", Project: "pricing", File: "round.go", Hunks: []string{"c2"}, Reviewed: true},
		core.CommitGateOverridden{Session: "pricing", Override: core.GateOverride{Project: "pricing", Gate: "ledger", Mode: core.GateHard, Unread: 38, Commit: "abc"}},
	} {
		if err := a.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	se, _ := a.State().Session("pricing")
	st := se.Snapshot()
	r := st.ReviewOf("pricing", "round.go")
	for key, want := range map[[2]string]core.LineState{
		{"c1", "c1.0"}: core.LineSkimmed, {"c1", "c1.1"}: core.LineRead, {"c1", "c1.2"}: core.LineSkimmed,
		{"c1", "c1.3"}: core.LineUnread, {"c2", "c2.7"}: core.LineRead,
	} {
		if got := r.State(key[0], key[1]); got != want {
			t.Errorf("%s is %s, want %s", key[1], got, want)
		}
	}
	if len(st.Overrides) != 1 || st.Overrides[0].Unread != 38 {
		t.Errorf("overrides: %+v", st.Overrides)
	}
	a.Apply(core.HunksReviewed{Session: "pricing", Project: "pricing", File: "round.go", Hunks: []string{"c2"}})
	se, _ = a.State().Session("pricing")
	if len(se.Snapshot().Reviews) != 1 {
		t.Error("taking the last mark off dropped the lines seen")
	}
	b := New()
	if err := core.Replay(bytes.NewReader(log.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed:\n%s\nwant:\n%s", sb, sa)
	}
	if err := a.Apply(core.LinesSeen{Session: "pricing", File: "round.go"}); err == nil {
		t.Error("seeing no lines applied")
	}
}

// The commit gate is hard for work and plan sessions, soft for jobs, off for reviews and direct sessions.
func TestDefaultGate(t *testing.T) {
	for k, want := range map[core.Kind]string{core.KindWork: "hard", core.KindPlan: "hard", core.KindJob: "soft",
		core.KindReview: "off", core.KindDirect: "off"} {
		if got := core.DefaultGate(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}
