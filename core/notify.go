package core

import (
	"fmt"
	"slices"
	"time"
)

func init() {
	RegisterEvent[Notified]("Notified")
	RegisterEvent[NoticeDismissed]("NoticeDismissed")
	RegisterEvent[SessionFocused]("SessionFocused")
	RegisterEvent[TabOpened]("TabOpened")
	RegisterEvent[TabClosed]("TabClosed")
}

// Severity is how much a notice matters.
type Severity string

const (
	SevInfo     Severity = "info"
	SevWarning  Severity = "warning"
	SevError    Severity = "error"
	SevQuestion Severity = "question"
)

// Action is a button on a notice: a command to run, with its args.
type Action struct {
	Title   string         `json:"title"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// Notice is anything the user is told: an engine message, a plugin's notify, a Lua error, a failed server.
// ID is stable ("vim.unsaved", "lsp.crashed"); Source says who sent it ("vim", "plugin:linear", "init.lua").
// Session is the session whose log it belongs in, or "" for Oriel's.
type Notice struct {
	ID       string    `json:"id"`
	Severity Severity  `json:"severity"`
	Source   string    `json:"source"`
	Session  SessionID `json:"session,omitempty"`
	Title    string    `json:"title,omitempty"` // a short heading: "Config error", "gopls restarted"
	Text     string    `json:"text"`
	Detail   string    `json:"detail,omitempty"`   // a stack trace, a command's stderr
	Location string    `json:"location,omitempty"` // file:line
	Actions  []Action  `json:"actions,omitempty"`
	Time     time.Time `json:"time"`
	Key      bool      `json:"key,omitempty"` // in answer to a key just typed, so it goes sooner
}

// Notification is one row of the history: a notice and how many times it has repeated.
type Notification struct {
	Notice
	Seq       uint64    `json:"seq"` // the event that first notified it; unique
	Count     int       `json:"count"`
	First     time.Time `json:"first"`
	Dismissed bool      `json:"dismissed,omitempty"`
}

// MaxNotifications is how many rows the history keeps; the oldest go first.
const MaxNotifications = 1000

// Notified adds a notice to the history. A notice with the same ID, source and session as a row already there
// folds into it: the count goes up, the row takes the new text and time and moves to the end, and it shows again.
// An error in a session that doesn't have focus marks that session.
type Notified struct {
	Notice
}

func (e Notified) Scope() SessionID { return e.Session }

func (e Notified) Apply(s *State) error {
	if e.ID == "" {
		return fmt.Errorf("notice has no id")
	}
	if e.Session != "" && e.Severity == SevError && e.Session != s.Focus {
		se := s.session(e.Session)
		if se == nil {
			return fmt.Errorf("no session %s", e.Session)
		}
		se.Unseen++
	}
	n := Notification{Notice: e.Notice, Seq: s.Seq + 1, Count: 1, First: e.Time}
	if i := slices.IndexFunc(s.Notifications, func(o Notification) bool {
		return o.ID == e.ID && o.Source == e.Source && o.Session == e.Session
	}); i >= 0 {
		old := s.Notifications[i]
		n.Seq, n.Count, n.First = old.Seq, old.Count+1, old.First
		s.Notifications = slices.Delete(s.Notifications, i, i+1)
	}
	s.Notifications = append(s.Notifications, n)
	if over := len(s.Notifications) - MaxNotifications; over > 0 {
		s.Notifications = slices.Delete(s.Notifications, 0, over)
	}
	return nil
}

// NoticeDismissed hides a row's toast; the row stays in the history. It names the row as Notified folds it, so it
// lands in that session's log and finds the row again when the log is replayed. Dismissal makes one.
type NoticeDismissed struct {
	ID      string    `json:"id"`
	Source  string    `json:"source,omitempty"`
	Session SessionID `json:"session,omitempty"`
}

// Dismissal is the event that dismisses n.
func Dismissal(n Notification) NoticeDismissed {
	return NoticeDismissed{ID: n.ID, Source: n.Source, Session: n.Session}
}

func (e NoticeDismissed) Scope() SessionID { return e.Session }

func (e NoticeDismissed) Apply(s *State) error {
	i := slices.IndexFunc(s.Notifications, func(n Notification) bool {
		return n.ID == e.ID && n.Source == e.Source && n.Session == e.Session
	})
	if i < 0 {
		return fmt.Errorf("no notification %q from %q in %q", e.ID, e.Source, e.Session)
	}
	s.Notifications[i].Dismissed = true
	return nil
}

// SessionFocused is the session now in front, or "" for none. Focusing a session clears its unseen errors, and
// opens a tab for it if it has none.
type SessionFocused struct {
	ID SessionID `json:"id"`
}

func (e SessionFocused) Scope() SessionID { return e.ID }

func (e SessionFocused) Apply(s *State) error {
	if e.ID != "" {
		se := s.session(e.ID)
		if se == nil {
			return fmt.Errorf("no session %s", e.ID)
		}
		se.Unseen = 0
		s.openTab(e.ID)
	}
	s.Focus = e.ID
	return nil
}

// TabOpened opens a tab for a session, behind the one in front.
type TabOpened struct {
	ID SessionID `json:"id"`
}

func (e TabOpened) Scope() SessionID { return e.ID }

func (e TabOpened) Apply(s *State) error {
	if s.session(e.ID) == nil {
		return fmt.Errorf("no session %s", e.ID)
	}
	s.openTab(e.ID)
	return nil
}

func (s *State) openTab(id SessionID) {
	if !slices.Contains(s.Tabs, id) {
		s.Tabs = append(slices.Clip(s.Tabs), id)
	}
}

// TabClosed closes a session's tab; the session stays. Closing the one in front leaves nothing in front.
type TabClosed struct {
	ID SessionID `json:"id"`
}

func (e TabClosed) Scope() SessionID { return "" }

func (e TabClosed) Apply(s *State) error {
	if !slices.Contains(s.Tabs, e.ID) {
		return fmt.Errorf("no tab for %s", e.ID)
	}
	s.Tabs = slices.DeleteFunc(slices.Clone(s.Tabs), func(id SessionID) bool { return id == e.ID })
	if s.Focus == e.ID {
		s.Focus = ""
	}
	return nil
}

// Counts is how many rows of the history are errors and warnings, among Oriel's and session's own.
// That's the status line's ✕ and ⚠.
func (s State) Counts(session SessionID) (errors, warnings int) {
	for _, n := range s.Notifications {
		if n.Session != "" && n.Session != session {
			continue
		}
		switch n.Severity {
		case SevError:
			errors++
		case SevWarning:
			warnings++
		}
	}
	return errors, warnings
}
