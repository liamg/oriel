package eventstore

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

var t0 = time.Date(2026, 10, 13, 14, 2, 11, 0, time.UTC)

func notice(id string, sev core.Severity, sess core.SessionID, at time.Duration) core.Notified {
	return core.Notified{Notice: core.Notice{ID: id, Severity: sev, Source: "test", Session: sess, Text: id, Time: t0.Add(at)}}
}

// errors.md: "The same ID repeating collapses into one entry with a count, so a hook failing on every
// keystroke is one row."
func TestRepeatsCollapse(t *testing.T) {
	s := New()
	s.Apply(notice("other", core.SevInfo, "", 0))
	for i := range 100 {
		if err := s.Apply(notice("lua.error", core.SevError, "", time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	st := s.State()
	if len(st.Notifications) != 2 {
		t.Fatalf("%d rows, want 2", len(st.Notifications))
	}
	n := st.Notifications[1]
	if n.ID != "lua.error" || n.Count != 100 || !n.First.Equal(t0) || !n.Time.Equal(t0.Add(99*time.Second)) {
		t.Errorf("row = %+v", n)
	}
	if n.Seq != 2 {
		t.Errorf("seq = %d, want the first notice's, 2", n.Seq)
	}
}

func TestRepeatShowsAgainAndMovesLast(t *testing.T) {
	s := New()
	s.Apply(notice("a", core.SevWarning, "", 0))
	s.Apply(notice("b", core.SevWarning, "", time.Second))
	if err := s.Apply(core.NoticeDismissed{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	s.Apply(notice("a", core.SevWarning, "", 2*time.Second))
	st := s.State()
	if got := st.Notifications[1]; got.ID != "a" || got.Dismissed || got.Count != 2 {
		t.Errorf("last row = %+v", got)
	}
}

func TestSameIDInAnotherSessionIsAnotherRow(t *testing.T) {
	s := New()
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	s.Apply(notice("lsp.crashed", core.SevWarning, "", 0))
	s.Apply(notice("lsp.crashed", core.SevWarning, "s1", 0))
	if n := len(s.State().Notifications); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

func TestHistoryIsCapped(t *testing.T) {
	s := New()
	for i := range core.MaxNotifications + 5 {
		s.Apply(notice(fmt.Sprint("n", i), core.SevInfo, "", 0))
	}
	st := s.State()
	if len(st.Notifications) != core.MaxNotifications || st.Notifications[0].ID != "n5" {
		t.Errorf("%d rows, first %s", len(st.Notifications), st.Notifications[0].ID)
	}
}

// errors.md: "Errors from a session you're not looking at don't toast: they put an error mark on that
// session's tab and wait in its log."
func TestErrorInUnfocusedSessionMarksIt(t *testing.T) {
	s := New()
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	s.Apply(core.SessionOpened{ID: "s2", Kind: core.KindWork, Status: core.StatusQuiet})
	s.Apply(core.SessionFocused{ID: "s1"})
	s.Apply(notice("e1", core.SevError, "s1", 0))
	s.Apply(notice("e2", core.SevError, "s2", 0))
	s.Apply(notice("w", core.SevWarning, "s2", 0))
	unseen := func(id core.SessionID) int { se, _ := s.Session(id); return se.Unseen() }
	if unseen("s1") != 0 || unseen("s2") != 1 {
		t.Errorf("unseen s1=%d s2=%d, want 0 1", unseen("s1"), unseen("s2"))
	}
	s.Apply(core.SessionFocused{ID: "s2"})
	if unseen("s2") != 0 {
		t.Errorf("focusing s2 left %d unseen", unseen("s2"))
	}
	if err := s.Apply(notice("x", core.SevError, "nope", 0)); err == nil {
		t.Error("notified a session that isn't open")
	}
}

// errors.md: "The status line's ✕/⚠ counts are the focused session's plus Oriel's."
func TestCounts(t *testing.T) {
	s := New()
	s.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	s.Apply(core.SessionOpened{ID: "s2", Kind: core.KindWork, Status: core.StatusQuiet})
	s.Apply(notice("a", core.SevError, "", 0))
	s.Apply(notice("b", core.SevWarning, "s1", 0))
	s.Apply(notice("c", core.SevWarning, "s1", 0))
	s.Apply(notice("d", core.SevError, "s2", 0))
	s.Apply(notice("e", core.SevInfo, "s1", 0))
	if e, w := s.State().Counts("s1"); e != 1 || w != 2 {
		t.Errorf("counts = %d %d, want 1 2", e, w)
	}
}

// errors.md: each notice "becomes a core event, Notified, so it lands in the event log"; replaying the log
// rebuilds the history.
func TestNotificationsReplay(t *testing.T) {
	var log bytes.Buffer
	a := New()
	a.Subscribe(core.Filter{}, core.NewRecorder(&log).Record)
	a.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet})
	n := notice("lua.error", core.SevError, "s1", 0)
	n.Detail, n.Location = "stack traceback:\n\tinit.lua:15", "init.lua:15"
	n.Actions = []core.Action{{Title: "Open at line", Command: "file.open", Args: map[string]any{"path": "init.lua", "line": 15.0}}}
	for range 3 {
		a.Apply(n)
	}
	a.Apply(notice("info", core.SevInfo, "", time.Second))
	a.Apply(core.NoticeDismissed{Seq: 5})
	a.Apply(core.SessionFocused{ID: "s1"})

	b := New()
	if err := core.Replay(&log, b); err != nil {
		t.Fatal(err)
	}
	if sa, sb := snapshot(t, a), snapshot(t, b); !bytes.Equal(sa, sb) {
		t.Errorf("replayed state:\n%s\nwant:\n%s", sb, sa)
	}
}
