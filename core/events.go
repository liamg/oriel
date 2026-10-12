package core

// SessionOpened adds a session to the store.
type SessionOpened struct {
	ID     SessionID
	Kind   Kind
	Status Status
}

func (e SessionOpened) Scope() SessionID { return e.ID }

// SessionStatusChanged moves a session to a new status.
type SessionStatusChanged struct {
	ID     SessionID
	Status Status
}

func (e SessionStatusChanged) Scope() SessionID { return e.ID }
