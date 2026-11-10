package persist

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
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
	env.Work.Wait() // the notices, emitted off the recorder
	if ns := failed(); len(ns) != 1 || ns[0].Count != 1 || ns[0].Severity != core.SevError {
		t.Errorf("notices of the failed log: %+v", ns)
	}
}

// A reply streaming in isn't logged at every chunk, each of which carries all of it so far: a few times as it comes,
// and whole once it's done. The log replays to the whole reply.
func TestAStreamingReplyIsLoggedWhole(t *testing.T) {
	st, env := hosttest.NewState(), hosttest.Env(t)
	l := NewLogs(env, st)
	st.Store.Subscribe(core.Filter{}, l.Record)
	id := core.SessionID("round-half-to-even")
	if err := st.Emit(core.SessionSynced{Session: core.SessionState{ID: id, Name: "Round half to even"}}); err != nil {
		t.Fatal(err)
	}
	reply := ""
	for i := range 50 {
		reply += fmt.Sprintf("word%d ", i)
		if err := st.Emit(core.TranscriptAppended{Session: id, Items: []core.Item{{ID: "m1", Kind: core.ItemAssistant, Text: reply, Partial: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Emit(core.TranscriptAppended{Session: id, Items: []core.Item{{ID: "m1", Kind: core.ItemAssistant, Text: reply}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.eventsPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"TranscriptAppended"`); n > 3 {
		t.Errorf("a reply in 50 chunks was logged %d times", n)
	}
	restored := eventstore.New()
	if _, err := core.ReplayCount(bytes.NewReader(b), restored); err != nil {
		t.Fatal(err)
	}
	if items := restoredItems(restored, id); len(items) != 1 || items[0].Text != reply || items[0].Partial {
		t.Errorf("the log replays to %+v", items)
	}
}

func restoredItems(s *eventstore.Store, id core.SessionID) []core.Item {
	se, _ := s.Session(id)
	if se == nil {
		return nil
	}
	return se.Snapshot().Transcript
}
