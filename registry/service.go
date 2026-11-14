package registry

import (
	"errors"
	"fmt"
	"time"

	"github.com/liamg/oriel/core"
)

// Service is an object a plugin provides for other plugins to call, by name: the editors,
// git's work trees, the code sites, the ships, the sessions. A later one of a name shadows it until it's disposed.
type Service struct {
	Name   string
	Impl   any
	Source string
}

func (Service) ContributionKind() string   { return "service" }
func (s Service) ContributionName() string { return s.Name }

// ServiceOf is the service registered as name, if there is one and it's a T.
func ServiceOf[T any](r Contributions, name string) (T, bool) {
	for _, s := range ContributionsOf[Service](r) {
		if s.Name == name {
			t, ok := s.Impl.(T)
			return t, ok
		}
	}
	var zero T
	return zero, false
}

// The built-in services' names.
const (
	SessionsService = "sessions"
)

// CodeSecretUnset is the JSON-RPC error a host request (http.request) answers when a secret it names isn't set:
// a plugin's process tests the code, not the message.
const CodeSecretUnset = -32010

// SecretUnset says err is a host request's answer that a secret it named isn't set: an error that knows its JSON-RPC
// code (jsonrpc.Coded) and says CodeSecretUnset.
func SecretUnset(err error) bool {
	var c interface{ RPCCode() int }
	return errors.As(err, &c) && c.RPCCode() == CodeSecretUnset
}

func SessionsOf(r Contributions) (Sessions, bool) { return ServiceOf[Sessions](r, SessionsService) }

// Sessions are the window's sessions.
type Sessions interface {
	Front() (core.SessionState, bool) // the session in front
	Get(id core.SessionID) (core.SessionState, bool)
	// FromArgs is the session a command's {id} names, else the one in front.
	FromArgs(a Args) (core.SessionState, error)
	// Start starts a session as session.start does with a, and answers its id.
	Start(ctx Ctx, a Args) (core.SessionID, error)
}

// SessionFromArgs is FromArgs over a session's id and the one in front: the shared body of Sessions.FromArgs.
func SessionFromArgs(a Args, front core.SessionID, get func(core.SessionID) (core.SessionState, bool)) (core.SessionState, error) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = front
	}
	if id == "" {
		return core.SessionState{}, errors.New("no session is in front")
	}
	se, ok := get(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %q", id)
	}
	return se, nil
}

// Load is how far reading something is: Done once it's read (or there's nothing to read), Err while the read fails,
// and since when it's been waiting.
type Load struct {
	Done  bool
	Err   error
	Since time.Time
}
