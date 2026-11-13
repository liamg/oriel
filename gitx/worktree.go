package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// FetchTimeout bounds each fetch a new worktree makes: over ssh it may wait on a key touch.
var FetchTimeout = 30 * time.Second

// Run runs git in the repo and returns what it printed.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	out, err := r.git(ctx, args...)
	return string(out), err
}

// TopLevel is the top of the work tree holding dir.
func TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := Repo{Dir: dir}.git(ctx, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(out)), err
}

// MainClone is the clone a worktree belongs to, or the repo itself when it is one. New worktrees are made from it.
func (r Repo) MainClone(ctx context.Context) string {
	if fi, err := os.Stat(filepath.Join(r.Dir, ".git")); err == nil && fi.IsDir() {
		return r.Dir
	}
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return r.Dir
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// HasBranch reports whether a local branch of this name exists.
func (r Repo) HasBranch(ctx context.Context, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// DefaultBranch guesses the repo's default branch: origin's HEAD, else the usual names.
func (r Repo) DefaultBranch(ctx context.Context) string {
	if out, err := r.git(ctx, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(string(out))
		if p := strings.TrimPrefix(ref, "refs/remotes/"); p != ref && p != "" {
			return p
		}
	}
	for _, c := range []string{"origin/main", "origin/master", "main", "master", "develop", "trunk"} {
		if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c
		}
	}
	return "HEAD"
}

// SyncedBase is the default branch, fetched and fast-forwarded first where git allows: where new work is cut from.
// With no remote or no network it's still returned, only not current.
func (r Repo) SyncedBase(ctx context.Context) string {
	def := strings.TrimPrefix(r.DefaultBranch(ctx), "origin/")
	if def == "" || def == "HEAD" {
		return r.DefaultBranch(ctx)
	}
	if ref, _ := r.SyncBranch(ctx, def); ref != "" {
		return ref
	}
	return r.DefaultBranch(ctx)
}

// SyncBranch brings one branch up to date with origin and returns the ref to cut from: the local branch,
// fast-forwarded where git allows, or origin's when there is no local one. "" when neither exists. The error says
// why a local branch couldn't be fast-forwarded; its ref is still returned.
func (r Repo) SyncBranch(ctx context.Context, name string) (string, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "origin/")
	if name == "" || name == "HEAD" {
		return "", nil
	}
	if !r.HasBranch(ctx, name) {
		r.fetch(ctx, name)
		if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); err == nil {
			return "origin/" + name, nil
		}
		return "", nil
	}
	if wt := r.BranchWorktree(ctx, name); wt != "" {
		r.fetch(ctx, name)
		if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); err != nil {
			return name, nil
		}
		_, err := Repo{Dir: wt}.git(ctx, "merge", "--ff-only", "origin/"+name)
		return name, err
	}
	if !r.fetch(ctx, name+":"+name) && r.fetch(ctx, name) {
		return name, fmt.Errorf("%s can't be fast-forwarded to origin/%s", name, name)
	}
	return name, nil
}

func (r Repo) fetch(ctx context.Context, refspec string) bool {
	if !r.HasRemote(ctx, "origin") {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	_, err := r.git(ctx, "fetch", "--quiet", "origin", refspec)
	return err == nil
}

// HasRemote reports whether the repo has a remote of this name.
func (r Repo) HasRemote(ctx context.Context, name string) bool {
	names, err := r.Remotes(ctx)
	return err == nil && slices.Contains(names, name)
}

// Remotes are the repo's remotes' names.
func (r Repo) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Clone clones url into dir, making its parents. Git doesn't prompt for a password here: a clone that needs one
// fails, saying what git said.
func Clone(ctx context.Context, url, dir string) error {
	return CloneProgress(ctx, url, dir, nil)
}

// RemoteURL is the URL of origin as it's configured (before any url.insteadOf), or "": none, or git couldn't say.
func (r Repo) RemoteURL(ctx context.Context) string {
	url, _ := r.Origin(ctx)
	return url
}

// Origin is the URL of origin as it's configured (before any url.insteadOf): "" with no error when there's none
// configured, and an error when git couldn't be asked (it failed, or ctx ended).
func (r Repo) Origin(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "config", "--get", "remote.origin.url")
	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(string(out)), nil
	case errors.As(err, &exit) && exit.ExitCode() == 1: // git config's "not set"
		return "", nil
	}
	return "", err
}

// BranchWorktree is the work tree that has branch checked out, or "".
func (r Repo) BranchWorktree(ctx context.Context, branch string) string {
	wts, _ := r.Worktrees(ctx)
	for _, w := range wts {
		if w.Branch == branch && !w.Detached && !w.Bare {
			return w.Dir
		}
	}
	return ""
}

// Worktree is one of a repo's work trees. Branch is its short name, "" when detached; Prunable means its directory
// is gone.
type Worktree struct {
	Dir, Branch, Head        string
	Prunable, Bare, Detached bool
}

// Worktrees are the repo's work trees, the main one first.
func (r Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	for line := range strings.SplitSeq(string(out), "\n") {
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			wts = append(wts, Worktree{Dir: val})
			continue
		}
		if len(wts) == 0 {
			continue
		}
		w := &wts[len(wts)-1]
		switch key {
		case "HEAD":
			w.Head = val
		case "branch":
			w.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			w.Bare = true
		case "detached":
			w.Detached = true
		case "prunable":
			w.Prunable = true
		}
	}
	return wts, nil
}

// PruneWorktrees forgets work trees whose directories are gone.
func (r Repo) PruneWorktrees(ctx context.Context) error {
	_, err := r.git(ctx, "worktree", "prune")
	return err
}

// FreshBranch is name, or name-2, name-3…: the first the repo has no branch called. An existing branch of the
// name holds earlier work, and new work must not start on top of it.
func (r Repo) FreshBranch(ctx context.Context, name string) string {
	out := name
	for n := 2; n < 100 && r.HasBranch(ctx, out); n++ {
		out = fmt.Sprintf("%s-%d", name, n)
	}
	return out
}

// AddWorktree makes a work tree at dir on a new branch cut from start, or detached at start when branch is "".
func (r Repo) AddWorktree(ctx context.Context, dir, branch, start string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add", "--quiet"}
	if branch == "" {
		args = append(args, "--detach", dir, start)
	} else {
		args = append(args, "-b", branch, dir, start)
	}
	_, err := r.git(ctx, args...)
	return err
}

// CheckoutWorktree makes a work tree at dir on branch, which already exists.
func (r Repo) CheckoutWorktree(ctx context.Context, dir, branch string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := r.git(ctx, "worktree", "add", "--quiet", dir, branch)
	return err
}

// RemoveWorktree takes a work tree away. Uncommitted work is refused unless force.
func (r Repo) RemoveWorktree(ctx context.Context, dir string, force bool) error {
	args := []string{"worktree", "remove", dir}
	if force {
		args = []string{"worktree", "remove", "--force", dir}
	}
	_, err := r.git(ctx, args...)
	return err
}

// Head is the commit checked out.
func (r Repo) Head(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// Dirty reports whether the work tree has uncommitted changes, or why git couldn't say. A caller guarding what would
// lose work refuses on the error; one only showing it counts the error as dirty.
func (r Repo) Dirty(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("reading %s's status: %w", r.Dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// ValidBranch says why git wouldn't take name for a new branch, or nil.
func ValidBranch(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("%q isn't a branch name", name)
	}
	if _, err := (Repo{Dir: os.TempDir()}).git(ctx, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("%q isn't a branch name git takes", name)
	}
	return nil
}
