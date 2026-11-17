// Package ship is the session host's side of code hosts: a session's projects on their way to a merged pull request
// (the Ship page), their pull requests on their code host, and review threads mirrored from it.
package ship

import (
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Ships is the host's code host side: each session's projects on their way to a merged pull request, and the review
// threads mirrored into them.
type Ships struct {
	env     hostcore.Env
	st      hostcore.State
	deps    Deps
	ship    shipState
	mirror  mirrorState
	remotes remotes
}

// Deps are what it needs of the rest of the host.
type Deps struct {
	Kinds hostcore.Kinds
	Keyed hostcore.Keyed
	// Wake hands a session's agent what's new, starting it if it isn't running.
	Wake func(id core.SessionID)
	// BranchRenamed records a project's branch's new name in its workspace's manifest.
	BranchRenamed func(root, project, name string) error
}

// New is the code host side of st's sessions.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Ships {
	return &Ships{env: env, st: st, deps: deps, ship: shipState{open: map[string]bool{}, synced: map[string]time.Time{}}}
}
