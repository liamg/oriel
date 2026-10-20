package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

var (
	// ErrUpToDate is a merge with nothing to bring: into has every commit of from's.
	ErrUpToDate = errors.New("nothing to merge")
	// ErrMergeConflict is a merge refused because it conflicts; nothing was changed.
	ErrMergeConflict = errors.New("the merge conflicts")
)

// Merge is one branch merged into another: where into was and is now, and the work tree moved with it, if any.
type Merge struct {
	Into, From  string
	Old, New    string
	FastForward bool
	Tree        string
}

// MergeInto merges branch from into branch into, never touching this work tree: a fast-forward where it can be, else
// a merge commit made without a work tree. A conflict is refused, naming the files, before anything moves. Where into
// is checked out, its work tree is fast-forwarded to the result, and only with no uncommitted changes to tracked
// files; elsewhere into's ref moves only if it hasn't moved since it was read. tree opens the work tree into is
// checked out in; nil opens it plainly.
func (r Repo) MergeInto(ctx context.Context, into, from string, tree func(dir string) (Repo, error)) (Merge, error) {
	m := Merge{Into: into, From: from}
	if into == "" || from == "" || into == from {
		return m, fmt.Errorf("can't merge %q into %q", from, into)
	}
	var err error
	if m.Old, err = r.rev(ctx, "refs/heads/"+into); err != nil {
		return m, fmt.Errorf("there's no branch %s", into)
	}
	head, err := r.rev(ctx, "refs/heads/"+from)
	if err != nil {
		return m, fmt.Errorf("there's no branch %s", from)
	}
	if r.ancestor(ctx, head, m.Old) {
		return m, fmt.Errorf("%w: %s has everything on %s", ErrUpToDate, into, from)
	}
	if r.ancestor(ctx, m.Old, head) {
		m.New, m.FastForward = head, true
	} else if m.New, err = r.mergeCommit(ctx, m.Old, head, fmt.Sprintf("Merge branch '%s' into %s", from, into)); err != nil {
		return m, err
	}
	m.Tree = r.BranchWorktree(ctx, into)
	if m.Tree == "" {
		_, err := r.git(ctx, "update-ref", "-m", "merge "+from, "refs/heads/"+into, m.New, m.Old)
		return m, err
	}
	t := Repo{Dir: m.Tree}
	if tree != nil {
		if t, err = tree(m.Tree); err != nil {
			return m, err
		}
	}
	if b, _ := t.Branch(ctx); b != into {
		return m, fmt.Errorf("%s is no longer on %s", m.Tree, into)
	}
	if out, err := t.git(ctx, "status", "--porcelain", "--untracked-files=no"); err != nil || strings.TrimSpace(string(out)) != "" {
		return m, fmt.Errorf("%s is checked out in %s with uncommitted changes: commit or stash them first", into, m.Tree)
	}
	if _, err := t.git(ctx, "merge", "--ff-only", "--quiet", m.New); err != nil {
		return m, fmt.Errorf("moving %s in %s: %w", into, m.Tree, err)
	}
	return m, nil
}

func (r Repo) rev(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return strings.TrimSpace(string(out)), err
}

// ancestor says a is reachable from b.
func (r Repo) ancestor(ctx context.Context, a, b string) bool {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// mergeTree writes the tree merging theirs into ours, without a work tree, or names the files it conflicts in.
func (r Repo) mergeTree(ctx context.Context, ours, theirs string) (tree string, conflicts []string, err error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", ours, theirs)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), r.Env...)
	out, err := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var exit *exec.ExitError
	switch {
	case err == nil:
		return lines[0], nil, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1 && len(lines) > 1:
		return "", slices.Compact(lines[1:]), nil
	}
	return "", nil, fmt.Errorf("git merge-tree: %w", err)
}

// mergeCommit is a commit merging theirs into ours, written without a work tree; a conflict wraps ErrMergeConflict.
func (r Repo) mergeCommit(ctx context.Context, ours, theirs, message string) (string, error) {
	tree, files, err := r.mergeTree(ctx, ours, theirs)
	switch {
	case err != nil:
		return "", err
	case len(files) > 0:
		return "", fmt.Errorf("%w in %s", ErrMergeConflict, strings.Join(files, ", "))
	}
	c, err := r.git(ctx, "commit-tree", tree, "-p", ours, "-p", theirs, "-m", message)
	return strings.TrimSpace(string(c)), err
}
