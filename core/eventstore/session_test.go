package eventstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

var when = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func sessionEvents() []core.Event {
	return []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing", Kind: core.KindWork, Status: core.StatusQuiet, Worker: core.LocalWorker,
			Dir: "/w/pricing", Branch: "liam/rounding", Created: when,
			Projects: []core.Project{{Name: "pricing", Repo: "/src/pricing", Dir: "/w/pricing/repos/pricing", Branch: "liam/rounding", Base: "main", Primary: true}}}},
		core.AgentChanged{ID: "pricing", Agent: core.AgentState{Provider: "claude", Model: "opus", Running: true}},
		core.ThreadUpdated{Session: "pricing", Thread: core.Thread{ID: "t1", Status: core.ThreadOpen, Created: when,
			Anchor:   &core.Anchor{Project: "pricing", File: "round.go", Line: 46},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorYou, Body: "negative values?", Time: when}}}},
		core.TaskChanged{Session: "pricing", Task: core.Task{ID: "1", Subject: "Negative tie handling", Status: core.TaskInProgress, Thread: "t1"}},
		core.TaskChanged{Session: "pricing", Task: core.Task{ID: "2", Subject: "scratch", Status: core.TaskPending}},
		core.TaskChanged{Session: "pricing", Task: core.Task{ID: "2", Status: core.TaskDeleted}},
		core.SubagentChanged{Session: "pricing", Subagent: core.Subagent{ID: "a1", Description: "explorer", Running: true}},
		core.AskOpened{Session: "pricing", Ask: core.Ask{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "go test ./..."}},
		core.AskClosed{Session: "pricing", ID: "p1", Decision: "allow"},
		core.TranscriptAppended{Session: "pricing", Items: []core.Item{{ID: "i1", Kind: core.ItemTool, Tool: "Bash", Title: "go test"}}},
		core.TranscriptAppended{Session: "pricing", Items: []core.Item{{ID: "i1", Kind: core.ItemTool, Tool: "Bash", Title: "go test", Done: true, Output: "ok"}}},
		core.TestRunRecorded{Session: "pricing", Run: core.TestRun{ID: "r1", Command: "go test ./...", Passed: 12, OK: true}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Windows: []core.UsageWindow{{Name: "5h", Used: 0.38}}}},
		core.WorkerChanged{Worker: core.WorkerState{ID: core.LocalWorker, Name: "mbp", Connected: true}},
		core.SessionRenamed{ID: "pricing", Name: "pricing-rounding"},
		core.ProjectsChanged{ID: "pricing", Branch: "liam/rounding-2", Projects: []core.Project{{Name: "pricing", Dir: "/w/pricing/repos/pricing", Branch: "liam/rounding-2"}},
			Past: []core.PastPR{{Project: "pricing", Branch: "liam/rounding", Number: 12}}},
	}
}

func TestSessionEventsReplay(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	for _, ev := range sessionEvents() {
		if err := a.Apply(ev); err != nil {
			t.Fatalf("%T: %v", ev, err)
		}
	}
	b := New()
	if err := core.Replay(bytes.NewReader(log.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed:\n%s\nwant:\n%s", sb, sa)
	}
	se, _ := a.State().Session("pricing")
	st := se.Snapshot()
	if len(st.Tasks) != 1 || len(st.Asks) != 0 || len(st.Transcript) != 1 || !st.Transcript[0].Done || st.Name != "pricing-rounding" {
		t.Errorf("state: %+v", st)
	}
}

// A copy of the state taken before an event must not change when the event applies: nested slices are shared.
func TestCopiesDontSeeLaterEvents(t *testing.T) {
	s := New()
	evs := sessionEvents()
	for _, ev := range evs[:3] {
		s.Apply(ev)
	}
	before := s.State()
	want := snapshotOf(t, before)
	for _, ev := range evs[3:] {
		s.Apply(ev)
	}
	s.Apply(core.ThreadUpdated{Session: "pricing", Thread: core.Thread{ID: "t1", Status: core.ThreadResolved}})
	if got := snapshotOf(t, before); !bytes.Equal(got, want) {
		t.Errorf("an old copy changed:\n%s\nwas:\n%s", got, want)
	}
}

func TestWorkerGoneTakesItsSessions(t *testing.T) {
	s := New()
	s.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Connected: true}})
	s.Apply(core.SessionSynced{Session: core.SessionState{ID: "a", Kind: core.KindWork, Worker: "devbox"}})
	s.Apply(core.SessionSynced{Session: core.SessionState{ID: "b", Kind: core.KindWork, Worker: core.LocalWorker}})
	s.Apply(core.SessionFocused{ID: "a"})
	if err := s.Apply(core.WorkerGone{ID: "devbox"}); err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if len(st.Sessions) != 1 || st.Sessions[0].ID != "b" || st.Focus != "" || st.Workers[0].Connected {
		t.Errorf("state after the worker went: %+v", st)
	}
}

// AI-15: a worker keeps the app's name for it and when it came or went; Forget takes an offline one off.
func TestWorkerLabelSinceAndForget(t *testing.T) {
	s := New()
	at := func(m int) time.Time { return when.Add(time.Duration(m) * time.Minute) }
	s.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Name: "devbox", Connected: true, Sampled: at(0)}})
	if err := s.Apply(core.WorkerRenamed{ID: "devbox", Label: "Build box"}); err != nil {
		t.Fatal(err)
	}
	s.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Name: "devbox", Connected: true, Sampled: at(5)}})
	w := s.State().Workers[0]
	if w.Title() != "Build box" || !w.Since.Equal(at(0)) || s.State().WorkerName("devbox") != "Build box" {
		t.Fatalf("after a reading: %+v", w)
	}
	if err := s.Apply(core.WorkerForgotten{ID: "devbox"}); err == nil {
		t.Fatal("a connected worker was forgotten")
	}
	s.Apply(core.WorkerGone{ID: "devbox", At: at(9)})
	if w := s.State().Workers[0]; w.Connected || !w.Since.Equal(at(9)) || w.Label != "Build box" {
		t.Fatalf("gone: %+v", w)
	}
	s.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Name: "devbox", Connected: true, Sampled: at(20)}})
	if w := s.State().Workers[0]; !w.Connected || !w.Since.Equal(at(20)) {
		t.Fatalf("back: %+v", w)
	}
	s.Apply(core.WorkerGone{ID: "devbox", At: at(30)})
	if err := s.Apply(core.WorkerForgotten{ID: "devbox"}); err != nil || len(s.State().Workers) != 0 {
		t.Fatalf("forget: %v, %+v", err, s.State().Workers)
	}
}

// AI-15: a worker speaks only for itself and its own sessions.
func TestFromWorker(t *testing.T) {
	s := New()
	s.Apply(core.SessionSynced{Session: core.SessionState{ID: "mine", Kind: core.KindWork, Worker: "devbox"}})
	s.Apply(core.SessionSynced{Session: core.SessionState{ID: "local", Kind: core.KindWork, Worker: core.LocalWorker}})
	st := s.State()
	for _, c := range []struct {
		ev   core.Event
		want bool
	}{
		{core.WorkerChanged{Worker: core.WorkerState{ID: "devbox"}}, true},
		{core.WorkerChanged{Worker: core.WorkerState{ID: core.LocalWorker}}, false},
		{core.WorkerGone{ID: core.LocalWorker}, false},
		{core.WorkerRenamed{ID: core.LocalWorker, Label: "x"}, false},
		{core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Workers: []core.WorkerID{"devbox"}}}, true},
		{core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Workers: []core.WorkerID{core.LocalWorker}}}, false},
		{core.SessionSynced{Session: core.SessionState{ID: "new", Worker: "devbox"}}, true},
		{core.SessionSynced{Session: core.SessionState{ID: "new", Worker: core.LocalWorker}}, false},
		{core.SessionSynced{Session: core.SessionState{ID: "local", Worker: "devbox"}}, false},
		{core.AgentChanged{ID: "mine"}, true},
		{core.AgentChanged{ID: "local"}, false},
		{core.AgentChanged{ID: "unknown"}, false},
		{core.SessionFocused{ID: "local"}, false},
		{core.Notified{Notice: core.Notice{ID: "n"}}, true},
	} {
		if got := st.FromWorker("devbox", c.ev); got != c.want {
			t.Errorf("%T %+v from devbox: %v, want %v", c.ev, c.ev, got, c.want)
		}
	}
	if !st.FromWorker(core.LocalWorker, core.AgentChanged{ID: "local"}) {
		t.Error("this machine's worker can't speak for its own session")
	}
}

func TestDeriveStatus(t *testing.T) {
	agent := core.AgentState{Provider: "claude", Running: true}
	you := core.Comment{Author: core.AuthorYou, Body: "x"}
	answer := core.Comment{Author: core.AuthorAgent, Body: "done"}
	note := core.Comment{Author: core.AuthorAgent, Body: "on it", Progress: true}
	open := func(cs ...core.Comment) []core.Thread {
		return []core.Thread{{ID: "t", Status: core.ThreadOpen, Comments: cs}}
	}
	for _, c := range []struct {
		name string
		se   core.SessionState
		want core.Status
	}{
		{"question beats permission", core.SessionState{Agent: agent, Asks: []core.Ask{{Kind: core.AskPermission}, {Kind: core.AskQuestion}}}, core.StatusNeedsYou},
		{"permission", core.SessionState{Agent: agent, Asks: []core.Ask{{Kind: core.AskNetwork}}}, core.StatusPermission},
		{"login gone beats permission", core.SessionState{Agent: agent, Asks: []core.Ask{{Kind: core.AskNetwork}, {Kind: core.AskLogin}}}, core.StatusNeedsYou},
		{"turn running", core.SessionState{Agent: core.AgentState{Provider: "claude", Running: true, Working: true}, Threads: open(answer)}, core.StatusWorking},
		{"subagent running", core.SessionState{Agent: agent, Subagents: []core.Subagent{{Running: true}}}, core.StatusWorking},
		{"agent answered", core.SessionState{Agent: agent, Threads: open(you, answer)}, core.StatusNeedsYou},
		{"progress note is not an answer", core.SessionState{Agent: agent, Threads: open(you, note)}, core.StatusWorking},
		{"queued for a running agent", core.SessionState{Agent: agent, Threads: open(you)}, core.StatusWorking},
		{"queued, nobody to do it", core.SessionState{Agent: core.AgentState{Provider: "claude"}, Threads: open(you)}, core.StatusNeedsYou},
		{"resolved threads are quiet", core.SessionState{Agent: agent, Threads: []core.Thread{{Status: core.ThreadResolved, Comments: []core.Comment{answer}}}}, core.StatusQuiet},
		{"failed turn", core.SessionState{Agent: core.AgentState{Provider: "claude", Running: true, Error: "overloaded"}}, core.StatusNeedsYou},
		{"archived", core.SessionState{Archived: true, Agent: agent, Threads: open(you)}, core.StatusOff},
		{"no agent", core.SessionState{Kind: core.KindWork}, core.StatusOff},
		{"direct, no agent", core.SessionState{Kind: core.KindDirect}, core.StatusQuiet},
	} {
		if got := core.DeriveStatus(c.se); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// AI-13: a worker's signed-out logins are the ones of an account's plan, by provider and account, on any worker.
func TestSignedOut(t *testing.T) {
	st := core.State{Workers: []core.WorkerState{
		{ID: "local", SignedOut: []core.Login{{Provider: "claude", Account: "u1/o1", Worker: "local"}}},
		{ID: "devbox", Name: "devbox", SignedOut: []core.Login{{Provider: "claude", Account: "u1/o1", Worker: "devbox"}, {Provider: "codex", Worker: "devbox"}}},
	}}
	if got := st.SignedOut(core.ProviderUsage{Provider: "claude", Account: "u1/o1"}); len(got) != 2 || got[1].Worker != "devbox" {
		t.Errorf("claude u1/o1: %+v", got)
	}
	if got := st.SignedOut(core.ProviderUsage{Provider: "claude", Account: "u2"}); len(got) != 0 {
		t.Errorf("another account: %+v", got)
	}
	if got := st.SignedOut(core.ProviderUsage{Provider: "codex", Account: "a@b"}); len(got) != 1 {
		t.Errorf("a login gone before its account was known counts for the provider's: %+v", got)
	}
}

func snapshotOf(t *testing.T, st core.State) []byte {
	t.Helper()
	s := New()
	s.state = st
	return snapshot(t, s)
}
