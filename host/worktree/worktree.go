// Package worktree is the session host's workspaces: a session's directory and the worktree of each repo it works
// in, made as its kind says when it starts, taken away when it ends, made again when it reopens, and moved on from
// one pull request to the next.
package worktree

import (
	"context"

	"github.com/liamg/oriel/agent"
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
	// NotesFiles are the agents' notes files a workspace keeps out of its git (agent.NotesFiles).
	NotesFiles func() []string
}

// New makes workspaces under env's home.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Worktrees {
	return &Worktrees{env: env, st: st, deps: deps}
}

// notesFiles are the agents' notes files, AGENTS.md alone with no Deps.NotesFiles.
func (x *Worktrees) notesFiles() []string {
	if x.deps.NotesFiles == nil {
		return agent.NotesFiles(nil)
	}
	return x.deps.NotesFiles()
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

// SaveProjects writes a workspace's projects back to its manifest, which a restore reads; a workspace with no
// manifest has nothing to write.
func (x *Worktrees) SaveProjects(id core.SessionID) error {
	se, err := x.st.Session(id)
	if err != nil {
		return err
	}
	m, ok := LoadManifest(se.Dir)
	if !ok {
		return nil
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
	return SaveManifest(se.Dir, m)
}

// recordProjects is SaveProjects after a change that's already made: a manifest it can't write doesn't undo the
// change, so it's said, not returned.
func (x *Worktrees) recordProjects(id core.SessionID) {
	if err := x.SaveProjects(id); err != nil {
		x.env.Log.Error("workspace manifest", "session", id, "err", err)
		_ = x.st.Emit(core.Notified{Notice: core.Notice{ID: "workspace.manifest", Severity: core.SevWarning, Source: "host",
			Session: id, Title: "Workspace not saved", Text: "Its branches couldn't be recorded, so restoring it may show old ones: " + err.Error(),
			Time: x.env.Now().UTC()}})
	}
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
