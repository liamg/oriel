package registry

import (
	"context"
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
	CodeSitesService = "code.sites"
	ShipsService     = "ships"
	SessionsService  = "sessions"

	// The session host's parts' calls, each its own service (host.SessionCalls, host.SandboxCalls…).
	HostSessionsService  = "host.sessions"
	HostSandboxesService = "host.sandboxes"
	HostShipsService     = "host.ships"
	HostCodeHostsService = "host.codehosts"
	HostReposService     = "host.repos"
	HostJobsService      = "host.jobs"
	HostStatsService     = "host.stats"
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

func CodeSitesOf(r Contributions) (CodeSites, bool) { return ServiceOf[CodeSites](r, CodeSitesService) }
func ShipsOf(r Contributions) (Ships, bool)         { return ServiceOf[Ships](r, ShipsService) }
func SessionsOf(r Contributions) (Sessions, bool)   { return ServiceOf[Sessions](r, SessionsService) }

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

// CodeSites say which code host, and which of its sites, a remote or a pull request's URL is on.
// They don't block but Probe, and are safe off the loop.
type CodeSites interface {
	URL(raw string) (HostSite, bool)                             // a pull request's URL
	Remote(remote string) (HostSite, string, bool)               // a remote's URL, and the repo's name there
	Probe(ctx context.Context, hostname string) (HostSite, bool) // asks each host whether hostname is one of its
	Named(hostname string) (string, bool)                        // the code host the user, gh or glab names hostname as
	Remember(hostname, name, api string) error                   // keeps which host hostname is, and its API URL
	ReviewSources() []HostSite                                   // the sites review requests are read from
	Keyed(s HostSite) bool                                       // s's token is set
	Warm(ctx context.Context, s HostSite)                        // reads s's token ahead of its first request
	Refuse(s HostSite, err error) bool                           // says s refused its token with err; true if it was refused
	TrustedUser(login string) bool                               // code.trusted_users has login
}

// Ships are what each of a session's projects has to ship, as the Ship page last read them.
type Ships interface {
	// For are se's projects as last read; one not read yet has only its branch.
	For(se core.SessionState) []ProjectShip
	// Refresh reads id's projects again, fresh past what's cached.
	Refresh(id core.SessionID, fresh bool)
	// ResolveThread resolves a thread mirrored from a code host there, or reopens it.
	ResolveThread(se core.SessionState, t core.Thread, resolved bool) error
	// ForwardThread posts the user's forwarded draft on a mirrored thread to its host.
	ForwardThread(se core.SessionState, thread core.ThreadID, comment string) error
}

// ProjectShip is one of a session's projects as Ship read it.
type ProjectShip struct {
	Project  string
	ReadOnly bool
	Files    []string // uncommitted
	Unpushed int      // commits not pushed
	Commits  int      // on the branch since its base
	Pushed   bool
	PR       *PR
	Code     *CodeHost // where origin is; nil for none Oriel knows
	Load     Load
}
