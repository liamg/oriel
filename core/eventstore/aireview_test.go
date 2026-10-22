package eventstore

import (
	"bytes"
	"testing"

	"github.com/liamg/oriel/core"
)

// AI reviews and their findings are session events: a finding is replaced by ID (dismissed, turned into a
// thread), the gate reads what's still open, and the log replays to the same state.
func TestAIReviewFindings(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	hi := core.Finding{ID: "f1", Review: "r1", Severity: "high", Category: "correctness", Title: "Historic invoices re-rounded", File: "billing/invoice.go", Line: 118}
	lo := core.Finding{ID: "f2", Review: "r1", Severity: "low", Category: "style", Title: "Magic number 2", File: "round.go", Line: 80}
	evs := []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing", Kind: core.KindWork}},
		core.AIReviewChanged{Session: "pricing", Review: core.AIReview{ID: "r1", Provider: "codex", Running: true}},
		core.FindingsRecorded{Session: "pricing", Findings: []core.Finding{lo, hi}},
	}
	for _, ev := range evs {
		if err := a.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	se, _ := a.State().Session("pricing")
	if ok, label := se.Snapshot().AIReviewGate(); ok || label != "running" {
		t.Errorf("gate while running: %v %q", ok, label)
	}
	a.Apply(core.AIReviewChanged{Session: "pricing", Review: core.AIReview{ID: "r1", Provider: "codex"}})
	se, _ = a.State().Session("pricing")
	st := se.Snapshot()
	if got := st.SortedFindings(); len(got) != 2 || got[0].ID != "f1" {
		t.Errorf("sorted: %+v", got)
	}
	if ok, label := st.AIReviewGate(); ok || label != "1 high or critical open" {
		t.Errorf("gate with a high finding: %v %q", ok, label)
	}
	hi.Dismissed = true
	if err := a.Apply(core.FindingsRecorded{Session: "pricing", Findings: []core.Finding{hi}}); err != nil {
		t.Fatal(err)
	}
	se, _ = a.State().Session("pricing")
	st = se.Snapshot()
	if len(st.Findings) != 2 || !st.Findings[1].Dismissed || len(st.AIReviews) != 1 {
		t.Errorf("after dismissing: %+v %+v", st.Findings, st.AIReviews)
	}
	if ok, label := st.AIReviewGate(); !ok || label != "clear · 1 minor open" {
		t.Errorf("gate: %v %q", ok, label)
	}
	b := New()
	if err := core.Replay(bytes.NewReader(log.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed:\n%s\nwant:\n%s", sb, sa)
	}
	if err := a.Apply(core.FindingsRecorded{Session: "pricing", Findings: []core.Finding{{ID: "f3"}}}); err == nil {
		t.Error("a finding with no title applied")
	}
}
