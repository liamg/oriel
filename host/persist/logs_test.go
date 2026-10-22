package persist

import (
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hosttest"
)

// A session log that stops taking events says so, once, in the session's notifications.
func TestALogThatFailsSaysSo(t *testing.T) {
	st, env := hosttest.NewState(), hosttest.Env(t)
	l := NewLogs(env, st)
	st.Store.Subscribe(core.Filter{}, l.Record)
	id := core.SessionID("round-half-to-even")
	if err := st.Emit(core.SessionSynced{Session: core.SessionState{ID: id, Name: "Round half to even"}}); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	l.recs[id].f.Close()
	l.mu.Unlock()
	failed := func() []core.Notification {
		var out []core.Notification
		for _, n := range st.Snapshot().Notifications {
			if n.Session == id && n.ID == "session.log" {
				out = append(out, n)
			}
		}
		return out
	}
	for i := range 3 {
		se, _ := st.Session(id)
		se.Agent.Stats.Turns = i + 1
		if err := st.Emit(core.AgentChanged{ID: id, Agent: se.Agent}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(failed()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if ns := failed(); len(ns) != 1 || ns[0].Count != 1 || ns[0].Severity != core.SevError {
		t.Errorf("notices of the failed log: %+v", ns)
	}
}
