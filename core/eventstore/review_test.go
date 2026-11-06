package eventstore

import (
	"bytes"
	"strings"
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

// Logs written while lines seen in the diff and commits past the commit gate were recorded still replay: those
// events change nothing now, and a hunk's mark beside them stands.
func TestRetiredReviewEventsReplay(t *testing.T) {
	log := strings.Join([]string{
		`{"seq":1,"type":"SessionSynced","event":{"session":{"id":"pricing","kind":"work"}}}`,
		`{"seq":2,"type":"LinesSeen","event":{"session":"pricing","project":"pricing","file":"round.go","read":["c1.1"],"skimmed":["c1.2"]}}`,
		`{"seq":3,"type":"HunksReviewed","event":{"session":"pricing","project":"pricing","file":"round.go","hunks":["c2"],"reviewed":true}}`,
		`{"seq":4,"type":"CommitGateOverridden","event":{"session":"pricing","override":{"project":"pricing","gate":"ledger","mode":"hard","unread":38,"commit":"abc"}}}`,
	}, "\n") + "\n"
	a := New()
	if err := core.Replay(strings.NewReader(log), a); err != nil {
		t.Fatal(err)
	}
	se, _ := a.State().Session("pricing")
	st := se.Snapshot()
	if got := st.ReviewedHunks("pricing", "round.go"); len(got) != 1 || !got["c2"] {
		t.Errorf("round.go's marks: %v", got)
	}
	if len(st.Reviews) != 1 {
		t.Errorf("reviews: %+v", st.Reviews)
	}
}
