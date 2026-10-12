package core

// SessionID names a session; it is unique across workers.
type SessionID string

// Kind is what a session is for. Plugins may define their own.
type Kind string

const (
	KindPlan   Kind = "plan"
	KindWork   Kind = "work"
	KindReview Kind = "review"
	KindTour   Kind = "tour"
	KindJob    Kind = "job"
)

// Status is a session's state at a glance, with the same colour everywhere (ui.md).
type Status string

const (
	StatusNeedsYou   Status = "needs_you"
	StatusPermission Status = "permission"
	StatusWorking    Status = "working"
	StatusCI         Status = "ci"
	StatusReviewer   Status = "reviewer"
	StatusQuiet      Status = "quiet"
)

// Store is session state, the thing every surface reads.
type Store interface {
	Sessions() []Session
	Session(id SessionID) (Session, bool)
	Apply(ev Event) error // the only mutation path; events come from the worker
	Subscribe(filter Filter, fn func(Event)) (cancel func())
	State() State // a copy; the store keeps changing
}

// Session is a read-only view of one session.
type Session interface {
	ID() SessionID
	Kind() Kind
	Status() Status
}

// Event is something that happened. Scope is the session it belongs to, or "" for app-wide events.
// Apply is the event's effect on state; it returns an error, and may leave s half-changed, when the event doesn't fit.
// Events are values that round-trip through JSON, registered with RegisterEvent so a log can be replayed.
type Event interface {
	Scope() SessionID
	Apply(s *State) error
}

// Filter selects events for a subscription. The zero Filter matches everything.
type Filter struct {
	Session SessionID
}

func (f Filter) Match(ev Event) bool {
	return f.Session == "" || f.Session == ev.Scope()
}
