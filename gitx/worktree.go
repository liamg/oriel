package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if ref := r.SyncBranch(ctx, def); ref != "" {
		return ref
	}
	return r.DefaultBranch(ctx)
}

// SyncBranch brings one branch up to date with origin and returns the ref to cut from: the local branch,
// fast-forwarded where git allows, or origin's when there is no local one. "" when neither exists.
func (r Repo) SyncBranch(ctx context.Context, name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "origin/")
	if name == "" || name == "HEAD" {
		return ""
	}
	if !r.HasBranch(ctx, name) {
		r.fetch(ctx, name)
		if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); err == nil {
			return "origin/" + name
		}
		return ""
	}
	if wt := r.BranchWorktree(ctx, name); wt != "" {
		r.fetch(ctx, name)
		_, _ = Repo{Dir: wt}.git(ctx, "merge", "--ff-only", "origin/"+name)
		return name
	}
	if !r.fetch(ctx, name+":"+name) {
		r.fetch(ctx, name)
	}
	return name
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
	out, err := r.git(ctx, "remote")
	if err != nil {
		return false
	}
	for _, l := range strings.Fields(string(out)) {
		if l == name {
			return true
		}
	}
	return false
}

// Clone clones url into dir, making its parents. Git doesn't prompt for a password here: a clone that needs one
// fails, saying what git said.
func Clone(ctx context.Context, url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--", url, dir)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cloning %s: %s", url, msg)
	}
	return nil
}

// RemoteURL is the URL of origin, or "".
func (r Repo) RemoteURL(ctx context.Context) string {
	out, err := r.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BranchWorktree is the work tree that has branch checked out, or "".
func (r Repo) BranchWorktree(ctx context.Context, branch string) string {
	out, err := r.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	current := ""
	for line := range strings.SplitSeq(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			current = strings.TrimSpace(p)
			continue
		}
		if b, ok := strings.CutPrefix(line, "branch "); ok && strings.TrimSpace(b) == "refs/heads/"+branch {
			return current
		}
	}
	return ""
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

// Dirty reports whether the work tree has uncommitted changes.
func (r Repo) Dirty(ctx context.Context) bool {
	out, err := r.git(ctx, "status", "--porcelain")
	return err == nil && strings.TrimSpace(string(out)) != ""
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
