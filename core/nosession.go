package core

import (
	"errors"
	"fmt"
)

// ErrNoSession is a session id that names no session.
var ErrNoSession = errors.New("no session")

// NoSession is ErrNoSession for id: `no session "s1"`.
func NoSession(id SessionID) error { return fmt.Errorf("%w %q", ErrNoSession, id) }
