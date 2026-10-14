package eventstore

import (
	"bytes"
	"testing"

	"github.com/liamg/oriel/core"
)

// RV-7: a verify quiz's result is a session event: it replays from the log, the last one stands, and a result
// that's none of passed, failed or skipped is refused.
func TestVerified(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	evs := []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing", Kind: core.KindWork}},
		core.Verified{Session: "pricing", Verification: core.Verification{Result: core.VerifyFailed, Questions: []core.QuizQuestion{
			{Question: "Which line decides a tie?", Answer: "none", Verdict: core.VerdictWrong, File: "round.go", Line: 42}}}},
		core.Verified{Session: "pricing", Verification: core.Verification{Result: core.VerifySkipped}},
	}
	for _, ev := range evs {
		if err := a.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	if err := a.Apply(core.Verified{Session: "pricing", Verification: core.Verification{Result: "maybe"}}); err == nil {
		t.Error("a result of maybe was kept")
	}
	se, _ := a.State().Session("pricing")
	if v, ok := se.Snapshot().Verification(); !ok || v.Result != core.VerifySkipped || len(se.Snapshot().Verifications) != 2 {
		t.Errorf("the last verification: %+v", se.Snapshot().Verifications)
	}
	b := New()
	if err := core.Replay(bytes.NewReader(log.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	se, _ = b.State().Session("pricing")
	if vs := se.Snapshot().Verifications; len(vs) != 2 || vs[0].Questions[0].Line != 42 || vs[0].Count(core.VerdictWrong) != 1 {
		t.Errorf("replayed: %+v", vs)
	}
}
