// Package worktree is the session host's workspaces: a session's directory and the worktree of each repo it works
// in, made as its kind says when it starts, taken away when it ends, made again when it reopens, and moved on from
// one pull request to the next.
package worktree

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Worktrees makes and moves sessions' workspaces.
type Worktrees struct {
	env  hostcore.Env
	st   hostcore.State
	deps Deps
}

// Deps are what it needs of the rest of the host.
type Deps struct {
	Kinds hostcore.Kinds
	// Resolve finds a registered repo by ID or path, registering a path it hasn't seen.
	Resolve func(ctx context.Context, ref string) (core.Repo, error)
	// AddRepo registers a checkout.
	AddRepo func(ctx context.Context, path string, src core.RepoSource) (core.Repo, error)
	// Repos are the registered checkouts.
	Repos func() []core.Repo
	// ReposDir is where new repos go when nothing else says.
	ReposDir func() string
}

// New makes workspaces under env's home.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Worktrees {
	return &Worktrees{env: env, st: st, deps: deps}
}

// worktrees are the session's projects that are worktrees Oriel made.
func worktrees(se core.SessionState) []core.Project {
	var out []core.Project
	for _, p := range se.Projects {
		if p.Repo != "" && p.Dir != p.Repo {
			out = append(out, p)
		}
	}
	return out
}

// SaveProjects writes a workspace's projects back to its manifest.
func (x *Worktrees) SaveProjects(id core.SessionID) {
	se, err := x.st.Session(id)
	if err != nil {
		return
	}
	m, ok := LoadManifest(se.Dir)
	if !ok {
		return
	}
	for i, e := range m.Projects {
		for _, p := range se.Projects {
			if p.Name == e.Name {
				m.Projects[i].Branch, m.Projects[i].Base, m.Projects[i].Commit = p.Branch, p.Base, p.Commit
			}
		}
		m.Projects[i].Past = nil
		for _, pr := range se.Past {
			if pr.Project == e.Name {
				m.Projects[i].Past = append(m.Projects[i].Past, ManifestPR{Branch: pr.Branch, Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State, ClosedAt: pr.Closed})
			}
		}
	}
	_ = SaveManifest(se.Dir, m)
}

// BranchRenamed records a project's branch's new name in the manifest of the workspace at root.
func BranchRenamed(root, project, name string) error {
	m, ok := LoadManifest(root)
	if !ok {
		return nil
	}
	for i := range m.Projects {
		if m.Projects[i].Name == project {
			m.Projects[i].Branch = name
		}
	}
	return SaveManifest(root, m)
}
