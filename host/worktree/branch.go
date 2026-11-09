package worktree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// NewBranchReq starts a fresh branch for a project: Name, else one made from the session's; Carry takes uncommitted
// work along, which is refused otherwise.
type NewBranchReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Name    string         `json:"name,omitempty"`
	Carry   bool           `json:"carry,omitempty"`
}

// NewBranch is a project's worktree on a fresh branch cut from its repo's default branch, freshly fetched, which
// the session takes as its branch for it. The branch it leaves is kept, commits and all. It answers the new name.
func (x *Worktrees) NewBranch(ctx context.Context, r NewBranchReq) (string, error) {
	se, p, repo, err := x.ownedProject(ctx, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	main := gitx.Repo{Dir: p.Repo}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = main.FreshBranch(ctx, BranchName(hostcore.WhoAmI(ctx), se.Ticket, string(se.ID)))
	} else if err := gitx.ValidBranch(ctx, name); err != nil {
		return "", err
	} else if main.HasBranch(ctx, name) {
		return "", fmt.Errorf("there's a branch %s already: pick another name", name)
	}
	if !r.Carry && repo.Dirty(ctx) {
		return "", fmt.Errorf("%s has uncommitted changes: commit them first, or start the new branch with carry to take them along", p.Name)
	}
	base := main.SyncedBase(ctx)
	if err := repo.CheckoutNew(ctx, name, base); err != nil {
		return "", fmt.Errorf("%s: %w", p.Name, err)
	}
	if err := hostcore.SetProjectBranch(x.st, se.ID, p.Name, name, base); err != nil {
		return "", err
	}
	x.SaveProjects(se.ID)
	return name, nil
}

// SwitchBack puts a project's worktree back on the session's branch for it, carrying uncommitted work over where
// git can.
func (x *Worktrees) SwitchBack(ctx context.Context, id core.SessionID, project string) error {
	_, p, repo, err := x.ownedProject(ctx, id, project)
	if err != nil {
		return err
	}
	if repo.CurrentBranch(ctx) == p.Branch {
		return fmt.Errorf("%s is on %s already", p.Name, p.Branch)
	}
	if err := repo.Switch(ctx, p.Branch); err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	return nil
}

// Adopt makes the branch a project's worktree is on the session's branch for it, when that's safe
// (hostcore.AdoptRefusal says when it isn't). Nothing adopts a branch but this, asked for.
func (x *Worktrees) Adopt(ctx context.Context, id core.SessionID, project string) error {
	se, p, repo, err := x.ownedProject(ctx, id, project)
	if err != nil {
		return err
	}
	if why := hostcore.AdoptRefusal(ctx, x.st, se, p, repo); why != "" {
		return fmt.Errorf("%s can't take its branch: %s", p.Name, why)
	}
	if err := hostcore.SetProjectBranch(x.st, se.ID, p.Name, repo.CurrentBranch(ctx), ""); err != nil {
		return err
	}
	x.SaveProjects(se.ID)
	return nil
}

// ownedProject is a project with a branch the session owns, in a worktree of its own.
func (x *Worktrees) ownedProject(ctx context.Context, id core.SessionID, project string) (core.SessionState, core.Project, gitx.Repo, error) {
	se, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, id, project)
	if err != nil {
		return se, p, repo, err
	}
	switch {
	case !x.deps.Kinds.Spec(se).Can(core.OwnsBranch):
		return se, p, repo, errors.New("this session works on your own checkout's branch: Oriel doesn't own it")
	case p.Repo == "" || p.Dir == p.Repo:
		return se, p, repo, fmt.Errorf("%s has no worktree of this session's", p.Name)
	case p.Branch == "" || p.Detached || p.ReadOnly:
		return se, p, repo, fmt.Errorf("%s has no branch of this session's", p.Name)
	}
	return se, p, repo, nil
}
