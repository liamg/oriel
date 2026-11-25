package worktree

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/internal/words"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// NextPR moves a project onto a fresh branch cut from its updated base, keeping the branch it leaves as a past PR.
// What's on the branch since its PR (uncommitted changes, and commits neither pushed, on the base nor the PR's) is
// the next PR's, so it comes along; where git can't carry it (a conflict with the base), the project is left as it
// was, on its branch, and the error says why.
func (x *Worktrees) NextPR(ctx context.Context, r hostapi.NextPRReq) error {
	se, p, repo, err := hostcore.ProjectRepo(ctx, x.env.Git, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	if p.Repo == "" || p.Dir == p.Repo {
		return fmt.Errorf("%s has no worktree of this session's to move on", p.Name)
	}
	branch, err := hostcore.OwnBranch(x.env.Git, p)
	if err != nil {
		return err
	}
	dirty, err := repo.Dirty(ctx)
	if err != nil {
		return err
	}
	main := gitx.Repo{Dir: p.Repo}
	base := main.SyncedBase(ctx)
	carry, err := leftBehind(ctx, repo, branch, base, r.Head)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	fresh := main.FreshBranch(ctx, BranchName(hostcore.WhoAmI(ctx), se.Ticket, se.ID)+"-next")
	if err := moveOnto(ctx, repo, branch, fresh, base, carry, dirty); err != nil {
		return fmt.Errorf("%s stays on %s: %w", p.Name, branch, err)
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
	x.recordProjects(se.ID)
	return nil
}

// leftBehind are the commits on branch that are on none of base, origin's copy of it, or head, oldest first.
func leftBehind(ctx context.Context, repo gitx.Repo, branch, base, head string) ([]string, error) {
	args := []string{"rev-list", "--reverse", branch}
	var not []string
	for _, ref := range []string{base, "refs/remotes/origin/" + branch, head} {
		if ref != "" && repo.Verify(ctx, ref) {
			not = append(not, ref)
		}
	}
	if len(not) > 0 {
		args = append(append(args, "--not"), not...)
	}
	out, err := repo.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// moveOnto checks out fresh, cut from base, with the commits carry picked onto it and, when dirty, the uncommitted
// changes brought over. Where git can't, it puts the worktree back on branch as it was.
func moveOnto(ctx context.Context, repo gitx.Repo, branch, fresh, base string, carry []string, dirty bool) error {
	if dirty {
		if _, err := repo.Run(ctx, "stash", "push", "--include-untracked", "--quiet", "-m", "oriel: carried to "+fresh); err != nil {
			return fmt.Errorf("couldn't put its uncommitted changes aside: %w", err)
		}
	}
	back := func(why string, err error) error {
		_, _ = repo.Run(ctx, "cherry-pick", "--abort")
		_, _ = repo.Run(ctx, "reset", "--hard", "--quiet")
		if dirty { // what a stash that didn't apply left of its untracked files: the stash still has them
			_, _ = repo.Run(ctx, "clean", "-fdq")
		}
		_, _ = repo.Run(ctx, "checkout", "--quiet", "--force", branch)
		_, _ = repo.Run(ctx, "branch", "--quiet", "-D", fresh)
		if dirty {
			_, _ = repo.Run(ctx, "stash", "pop", "--quiet")
		}
		return fmt.Errorf("%s: %w", why, err)
	}
	if err := repo.CheckoutNew(ctx, fresh, base); err != nil {
		return back("couldn't cut "+fresh, err)
	}
	if len(carry) > 0 {
		if _, err := repo.Run(ctx, append([]string{"cherry-pick", "--allow-empty"}, carry...)...); err != nil {
			return back(fmt.Sprintf("its %s since the PR %s on %s", words.Counted(len(carry), "commit"), words.Plural(len(carry), "doesn't apply", "don't apply"),
				strings.TrimPrefix(base, "origin/")), err)
		}
	}
	if dirty {
		if _, err := repo.Run(ctx, "stash", "pop", "--quiet"); err != nil {
			return back("its uncommitted changes don't apply on "+strings.TrimPrefix(base, "origin/"), err)
		}
	}
	return nil
}
