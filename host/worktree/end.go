package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// CheckClean refuses a session whose worktrees have uncommitted work: removing them would lose it.
func CheckClean(ctx context.Context, se core.SessionState) error {
	for _, p := range worktrees(se) {
		if dirty, err := (gitx.Repo{Dir: p.Dir}).Dirty(ctx); err != nil {
			return fmt.Errorf("%s: can't tell whether it has uncommitted work: %w", p.Name, err)
		} else if dirty {
			return fmt.Errorf("%s has uncommitted work: commit or discard it first", p.Name)
		}
	}
	return nil
}

// Archive removes a session's worktrees, whatever is in them when discard is set, and its sandbox's leftovers, and
// marks it archived: its threads, transcript and test runs stay.
func (x *Worktrees) Archive(ctx context.Context, se core.SessionState, discard bool) error {
	if root := hostcore.AgentDir(se); root != "" {
		if err := sandbox.Clean(root); err != nil {
			x.env.Log.Warn("archive: sandbox cleanup", "session", se.ID, "err", err)
		}
	}
	for _, p := range worktrees(se) {
		if err := (gitx.Repo{Dir: p.Repo}).RemoveWorktree(ctx, p.Dir, discard); err != nil && !errors.Is(err, os.ErrNotExist) {
			if _, serr := os.Stat(p.Dir); serr == nil {
				return err
			}
		}
	}
	if err := x.st.Emit(core.SessionArchived{ID: se.ID, Archived: true}); err != nil {
		return err
	}
	hostcore.Restatus(x.st, se.ID)
	return nil
}

// Reopen makes an archived session's worktrees again, on their branches, or pinned where they were.
func Reopen(ctx context.Context, se core.SessionState) error {
	for _, p := range worktrees(se) {
		if _, err := os.Stat(p.Dir); err == nil {
			continue
		}
		g := gitx.Repo{Dir: p.Repo}
		_ = g.PruneWorktrees(ctx)
		var err error
		switch {
		case p.Detached:
			err = g.AddWorktree(ctx, p.Dir, "", p.Commit)
		case g.HasBranch(ctx, p.Branch):
			err = g.CheckoutWorktree(ctx, p.Dir, p.Branch)
		default:
			err = fmt.Errorf("its branch %s is gone", p.Branch)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	return nil
}

// Delete throws a session's workspace away: its worktrees with whatever is in them, the workspace, and its sandbox.
// The repos themselves are never touched, nor a directory that isn't a workspace of Oriel's.
func (x *Worktrees) Delete(ctx context.Context, se core.SessionState) error {
	for _, p := range worktrees(se) {
		if err := (gitx.Repo{Dir: p.Repo}).RemoveWorktree(ctx, p.Dir, true); err != nil {
			if _, serr := os.Stat(p.Dir); serr == nil {
				return fmt.Errorf("%s: %w", p.Name, err)
			}
		}
	}
	if se.Dir != "" && hostcore.Within(se.Dir, x.env.WorkspacesDir()) {
		if err := os.RemoveAll(se.Dir); err != nil {
			return err
		}
	}
	if root := hostcore.AgentDir(se); root != "" {
		if err := sandbox.Remove(root); err != nil {
			x.env.Log.Warn("delete: sandbox", "session", se.ID, "err", err)
		}
	}
	return x.st.Emit(core.SessionClosed{ID: se.ID})
}

// DeleteBranches deletes each project's local branch, but a repo's default branch.
func DeleteBranches(ctx context.Context, se core.SessionState) error {
	var failed []string
	for _, p := range worktrees(se) {
		if p.Branch == "" || p.Detached {
			continue
		}
		g := gitx.Repo{Dir: p.Repo}
		if p.Branch == strings.TrimPrefix(g.DefaultBranch(ctx), "origin/") || !g.HasBranch(ctx, p.Branch) {
			continue
		}
		if err := g.DeleteBranch(ctx, p.Branch, true); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", p.Name, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the branches stayed: %s", strings.Join(failed, "; "))
	}
	return nil
}
