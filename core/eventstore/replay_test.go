package eventstore

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

func snapshot(t *testing.T, s *Store) []byte {
	t.Helper()
	b, err := json.MarshalIndent(s.State(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestReplayRecordedLog(t *testing.T) {
	f, err := os.Open("testdata/sessions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := New()
	if err := core.Replay(f, s); err != nil {
		t.Fatal(err)
	}
	got := snapshot(t, s)
	if *update {
		os.WriteFile("testdata/sessions.state.json", got, 0o644)
	}
	want, err := os.ReadFile("testdata/sessions.state.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("replayed state:\n%s\nwant:\n%s", got, want)
	}
}

func TestRecordThenReplay(t *testing.T) {
	var log1, log2 bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log1).Record)
	for _, ev := range []core.Event{
		core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "s2", Kind: core.KindPlan, Status: core.StatusWorking},
		core.SessionStatusChanged{ID: "s1", Status: core.StatusNeedsYou},
		core.SessionStatusChanged{ID: "nope", Status: core.StatusCI}, // rejected, so not recorded
		core.SessionClosed{ID: "s2"},
		core.SessionOpened{ID: "s3", Kind: "notes", Status: core.StatusQuiet},
	} {
		a.Apply(ev)
	}

	b := New()
	b.Subscribe(core.Filter{}, core.NewRecorder(&log2).Record)
	if err := core.Replay(bytes.NewReader(log1.Bytes()), b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed state:\n%s\nwant:\n%s", sb, sa)
	}
	if !bytes.Equal(log1.Bytes(), log2.Bytes()) {
		t.Errorf("re-recorded log:\n%s\nwant:\n%s", log2.Bytes(), log1.Bytes())
	}
	if n := strings.Count(log1.String(), "\n"); n != 5 {
		t.Errorf("recorded %d events, want 5", n)
	}
}

// noted is an event a plugin defines: it has no effect on core state, but replays like any other.
type noted struct {
	Session core.SessionID `json:"session"`
	Text    string         `json:"text"`
}

func (n noted) Scope() core.SessionID     { return n.Session }
func (n noted) Apply(s *core.State) error { return nil }
func (noted) Logged()                     {}

// Event types are registered as their package starts, as a plugin's are.
func init() { core.RegisterEvent[noted]("test.Noted") }

func TestPluginEventsReplay(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	a.Apply(noted{Session: "s1", Text: "hi"})

	var got []core.Event
	b := New()
	b.Subscribe(core.Filter{}, func(ev core.Event) { got = append(got, ev) })
	if err := core.Replay(&log, b); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (noted{Session: "s1", Text: "hi"}) {
		t.Errorf("replayed %#v", got)
	}
}

func TestReplayRejectsBadLogs(t *testing.T) {
	for name, log := range map[string]string{
		"unknown type": `{"seq":1,"type":"NoSuchEvent","event":{}}` + "\n",
		"seq gap": `{"seq":1,"type":"SessionOpened","event":{"id":"s1","kind":"work","status":"quiet"}}` + "\n" +
			`{"seq":3,"type":"SessionClosed","event":{"id":"s1"}}` + "\n",
		"rejected event": `{"seq":1,"type":"SessionClosed","event":{"id":"s1"}}` + "\n",
		"not json":       "nope\n",
	} {
		if err := core.Replay(strings.NewReader(log), New()); err == nil {
			t.Errorf("%s: replay succeeded", name)
		}
	}
}

func TestRejectedEventLeavesStateAlone(t *testing.T) {
	s := New()
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	before := snapshot(t, s)
	if err := s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindPlan, Status: core.StatusCI}); err == nil {
		t.Fatal("opened s1 twice")
	}
	if after := snapshot(t, s); !bytes.Equal(before, after) {
		t.Errorf("state changed:\n%s\nwas:\n%s", after, before)
	}
}
