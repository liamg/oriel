// Package github is the session host's side of code hosts: a session's projects on their way to a merged pull request
// (the Ship page), their pull requests on GitHub, and review threads mirrored from GitHub or another code host.
package github

import (
	"net/http"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/hostcore"
)

// GitHub is the host's code host side.
type GitHub struct {
	env     hostcore.Env
	st      hostcore.State
	deps    Deps
	ship    shipState
	mirror  mirrorState
	clients gh.Clients
	remotes remotes
}

// Deps are what it needs of the rest of the host.
type Deps struct {
	Kinds hostcore.Kinds
	Keyed hostcore.Keyed
	// Client is the GitHub client for a checkout, with the git guard's env; nil for GitHub's API with the user's token.
	Client func() func(dir string, env []string) gh.Client
	// HTTP is the client GitHub's API is asked through; http.DefaultClient if nil, or it gives nil.
	HTTP func() *http.Client
	// Wake hands a session's agent what's new, starting it if it isn't running.
	Wake func(id core.SessionID)
	// BranchRenamed records a project's branch's new name in its workspace's manifest.
	BranchRenamed func(root, project, name string) error
}

// New is the code host side of st's sessions.
func New(env hostcore.Env, st hostcore.State, deps Deps) *GitHub {
	x := &GitHub{env: env, st: st, deps: deps, ship: shipState{github: map[string]githubRead{}, synced: map[string]time.Time{}}}
	x.clients = gh.Clients{HTTP: deps.HTTP, Client: func(dir string, env []string) gh.Client {
		if c := deps.Client(); c != nil {
			return c(dir, env)
		}
		return nil
	}}
	return x
}

// PROpen says the last GitHub reading of a branch found its PR open, whose page shows the PR rather than a draft.
func (x *GitHub) PROpen(id core.SessionID, project, branch string) bool {
	x.ship.mu.Lock()
	defer x.ship.mu.Unlock()
	g, ok := x.ship.github[shipKey(id, project)+"/"+branch]
	return ok && g.pr.Open()
}
