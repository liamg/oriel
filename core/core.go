package core

// SessionID names a session; it is unique.
type SessionID string

// Kind is what a session is for. Plugins may define their own.
type Kind string

const (
	KindPlan   Kind = "plan"
	KindWork   Kind = "work"
	KindReview Kind = "review"
	KindJob    Kind = "job"
)

// Status is a session's state at a glance, with the same colour everywhere.
type Status string

const (
	StatusNeedsYou   Status = "needs_you"
	StatusPermission Status = "permission"
	StatusWorking    Status = "working"
	StatusCI         Status = "ci"
	StatusReviewer   Status = "reviewer"
	StatusQuiet      Status = "quiet"
	StatusOff        Status = "off"
)

// Store is session state, the thing every surface reads.
type Store interface {
	Sessions() []Session
	Session(id SessionID) (Session, bool)
	Apply(ev Event) error // the only mutation path; events come from the session host and the app
	Subscribe(filter Filter, fn func(Event)) (cancel func())
	State() State     // as it stands, read-only: the store moves on to a new one, never changes this
	Focus() SessionID // the session in front, without copying the state
}

// Session is a read-only view of one session.
type Session interface {
	ID() SessionID
	Name() string // what its tab says: its name, or its id
	Kind() Kind
	Status() Status
	Unseen() int // errors since it last had focus: its tab's error mark
	Projects() []Project
	Threads() []Thread
	Tasks() []Task
	Agent() AgentState
	Snapshot() SessionState // all of it, as a copy
}

// Event is something that happened. Scope is the session it belongs to, or "" for app-wide events.
// Apply is the event's effect on state; it returns an error, and may leave s half-changed, when the event doesn't fit.
// Events are values that round-trip through JSON, registered with RegisterEvent so a log can be replayed.
type Event interface {
	Scope() SessionID
	Apply(s *State) error
}

// Logged is an event that's published and logged even when it leaves the state as it was: a plugin's own record.
// Any other event that changes nothing is dropped.
type Logged interface {
	Event
	Logged()
}

// Filter selects events for a subscription. The zero Filter matches everything.
type Filter struct {
	Session SessionID
}

func (f Filter) Match(ev Event) bool {
	return f.Session == "" || f.Session == ev.Scope()
}
