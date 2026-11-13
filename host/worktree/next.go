package worktree

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// NextPRReq moves one project of a session on from its PR: Done is that PR as its code host has it, and Head the
// commit it was at, whose commits count as the PR's even once it's squashed and its branch deleted.
type NextPRReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Done    core.PastPR    `json:"done,omitzero"`
	Head    string         `json:"head,omitempty"`
}

// NextPR moves a project onto a fresh branch cut from its updated base, keeping the branch it leaves as a past PR.
// It refuses while that branch has uncommitted changes, or commits that are neither pushed nor on the base.
func (x *Worktrees) NextPR(ctx context.Context, r NextPRReq) error {
	se, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	if p.Repo == "" || p.Dir == p.Repo {
		return fmt.Errorf("%s has no worktree of this session's to move on", p.Name)
	}
	branch, err := hostcore.OwnBranch(p)
	if err != nil {
		return err
	}
	if dirty, err := repo.Dirty(ctx); err != nil {
		return err
	} else if dirty {
		return fmt.Errorf("%s has uncommitted changes on %s: commit or discard them first", p.Name, branch)
	}
	main := gitx.Repo{Dir: p.Repo}
	base := main.SyncedBase(ctx)
	n, err := leftBehind(ctx, repo, branch, base, r.Head)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	if n > 0 {
		return fmt.Errorf("%s has %d commit%s on %s that aren't pushed or on %s: push them, or drop them, first",
			p.Name, n, hostcore.Plural(n), branch, strings.TrimPrefix(base, "origin/"))
	}
	fresh := main.FreshBranch(ctx, BranchName(hostcore.WhoAmI(ctx), se.Ticket, string(se.ID))+"-next")
	if err := repo.CheckoutNew(ctx, fresh, base); err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	done := r.Done
	done.Project, done.Branch, done.Closed = p.Name, branch, x.env.Now().UTC()
	projects := slices.Clone(se.Projects)
	for i := range projects {
		if projects[i].Name == p.Name {
			projects[i].Branch, projects[i].Base = fresh, base
		}
	}
	b := se.Branch
	if pp, ok := (core.SessionState{Projects: projects}).Primary(); ok {
		b = pp.Branch
	}
	if err := x.st.Emit(core.ProjectsChanged{ID: se.ID, Branch: b, Projects: projects, Past: append(slices.Clone(se.Past), done), Started: se.Started}); err != nil {
		return err
	}
	for _, kind := range []string{"commit", "pr"} {
		x.env.DropDraft(se.ID, p.Name, kind)
	}
	x.SaveProjects(se.ID)
	return nil
}

// leftBehind counts the commits on branch that are on none of base, origin's copy of it, or head.
func leftBehind(ctx context.Context, repo gitx.Repo, branch, base, head string) (int, error) {
	var not []string
	for _, ref := range []string{base, "refs/remotes/origin/" + branch, head} {
		if ref != "" && repo.Verify(ctx, ref) {
			not = append(not, ref)
		}
	}
	return repo.CountCommits(ctx, branch, not...)
}
