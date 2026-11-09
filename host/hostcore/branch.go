package hostcore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// OffBranch is a project whose worktree is on a branch other than the session's for it, which Oriel won't push.
type OffBranch struct {
	Session string `json:"session"`           // the session's branch for the project
	Current string `json:"current,omitempty"` // the branch checked out; "" when detached
	Refused string `json:"refused,omitempty"` // why Current can't be made the session's branch; "" when it can
}

// OffBranchOf is p's worktree off the session's branch, or nil while it's on it or p has no branch of the session's.
func OffBranchOf(ctx context.Context, st State, se core.SessionState, p core.Project, repo gitx.Repo) *OffBranch {
	if p.Branch == "" || p.Detached || p.ReadOnly {
		return nil
	}
	cur := repo.CurrentBranch(ctx)
	if cur == p.Branch {
		return nil
	}
	if _, err := repo.Head(ctx); cur == "" && err != nil {
		return nil
	}
	return &OffBranch{Session: p.Branch, Current: cur, Refused: adoptRefusal(ctx, st, se, p, repo, cur)}
}

// adoptRefusal is why cur can't become the session's branch for p, or "". It must be a branch; not the repo's
// default branch or the base p merges into; no other session's; holding every commit of the session's branch, so
// nothing of the session's is left behind (a branch gone, renamed with git, holds them); and not on origin already,
// where it may be someone else's that a push would replace.
func adoptRefusal(ctx context.Context, st State, se core.SessionState, p core.Project, repo gitx.Repo, cur string) string {
	if cur == "" {
		return "the worktree is detached: there's no branch to make the session's"
	}
	if def := strings.TrimPrefix(repo.DefaultBranch(ctx), "origin/"); cur == def {
		return cur + " is the repo's default branch"
	}
	if cur == strings.TrimPrefix(p.Base, "origin/") {
		return cur + " is the branch " + p.Name + " merges into"
	}
	if who := branchOwner(st, se.ID, p, cur); who != "" {
		return fmt.Sprintf("%s is another session's branch (%s)", cur, who)
	}
	if repo.HasBranch(ctx, p.Branch) && !repo.Ancestor(ctx, "refs/heads/"+p.Branch, "refs/heads/"+cur) {
		return fmt.Sprintf("%s doesn't hold this session's commits on %s: switch back, or bring them over first", cur, p.Branch)
	}
	if repo.Verify(ctx, "refs/remotes/origin/"+cur) {
		return fmt.Sprintf("origin has a branch %s already, which may not be this session's", cur)
	}
	return ""
}

// branchOwner names the other session whose project of p's repo is on branch, or "".
func branchOwner(st State, self core.SessionID, p core.Project, branch string) string {
	repo := cmp.Or(p.Repo, p.Dir)
	for _, se := range st.Snapshot().Sessions {
		if se.ID == self {
			continue
		}
		if slices.ContainsFunc(se.Projects, func(q core.Project) bool { return q.Branch == branch && cmp.Or(q.Repo, q.Dir) == repo }) {
			return cmp.Or(se.Name, string(se.ID))
		}
	}
	return ""
}

// AdoptRefusal is why p's worktree's branch can't become the session's, or "" when it can.
func AdoptRefusal(ctx context.Context, st State, se core.SessionState, p core.Project, repo gitx.Repo) string {
	off := OffBranchOf(ctx, st, se, p, repo)
	if off == nil {
		return p.Name + " is on this session's branch already"
	}
	return off.Refused
}

// SetProjectBranch records a project's branch, and its base when base isn't "", under the session's edit lock. The
// session's own branch follows when it was the old one and no other project is still on that.
func SetProjectBranch(st State, id core.SessionID, project, branch, base string) error {
	return st.Mutate(id, func(se core.SessionState) error {
		i := slices.IndexFunc(se.Projects, func(p core.Project) bool { return p.Name == project })
		if i < 0 {
			return fmt.Errorf("session %s has no project %q", id, project)
		}
		projects := slices.Clone(se.Projects)
		old := projects[i].Branch
		projects[i].Branch = branch
		if base != "" {
			projects[i].Base = base
		}
		b := se.Branch
		if b == old && !slices.ContainsFunc(projects, func(q core.Project) bool { return q.Branch == old }) {
			b = branch
		}
		return st.Emit(core.ProjectsChanged{ID: se.ID, Branch: b, Projects: projects, Past: se.Past, Started: se.Started})
	})
}
