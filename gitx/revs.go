package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Init makes a repository at dir, on branch when it isn't "".
func Init(ctx context.Context, dir, branch string) error {
	args := []string{"init", "--quiet"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	_, err := Repo{Dir: dir}.git(ctx, args...)
	return err
}

// UserName is the user's name as git has it (user.name), or "".
func UserName(ctx context.Context) string {
	out, err := Repo{}.git(ctx, "config", "--get", "user.name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CloneDepth clones url into dir with only the last depth commits.
func CloneDepth(ctx context.Context, url, dir string, depth int) error {
	_, err := Repo{}.git(ctx, "clone", "--quiet", "--depth", strconv.Itoa(depth), "--", url, dir)
	return err
}

// Rev is the commit ref names, or an error when it names none.
func (r Repo) Rev(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return strings.TrimSpace(string(out)), err
}

// Verify says ref names a commit.
func (r Repo) Verify(ctx context.Context, ref string) bool {
	_, err := r.Rev(ctx, ref)
	return err == nil
}

// MergeBaseOf is the best common ancestor of a and b.
func (r Repo) MergeBaseOf(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	return strings.TrimSpace(string(out)), err
}

// CountCommits counts the commits reachable from from and from none of not.
func (r Repo) CountCommits(ctx context.Context, from string, not ...string) (int, error) {
	args := []string{"rev-list", "--count", from}
	if len(not) > 0 {
		args = append(append(args, "--not"), not...)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// CheckoutNew makes branch at start and checks it out.
func (r Repo) CheckoutNew(ctx context.Context, branch, start string) error {
	_, err := r.git(ctx, "checkout", "--quiet", "-b", branch, start)
	return err
}

// Switch checks out an existing branch, carrying uncommitted changes over where git can.
func (r Repo) Switch(ctx context.Context, branch string) error {
	if !r.HasBranch(ctx, branch) {
		return fmt.Errorf("there's no branch %q", branch)
	}
	_, err := r.git(ctx, "checkout", "--quiet", branch, "--")
	return err
}

// Ancestor says a is reachable from b: b holds every commit of a.
func (r Repo) Ancestor(ctx context.Context, a, b string) bool {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Detach checks out rev with no branch.
func (r Repo) Detach(ctx context.Context, rev string) error {
	_, err := r.git(ctx, "checkout", "--quiet", "--detach", rev)
	return err
}

// FetchRef fetches origin's from into the local ref to, moving it however from moved.
func (r Repo) FetchRef(ctx context.Context, from, to string) error {
	_, err := r.git(ctx, "fetch", "--quiet", "origin", "+"+from+":"+to)
	return err
}

// ChangedFiles are the paths that differ between base and the work tree.
func (r Repo) ChangedFiles(ctx context.Context, base string) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", base)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// CurrentBranch is the branch checked out, or "" on a detached HEAD.
func (r Repo) CurrentBranch(ctx context.Context) string {
	out, _ := r.git(ctx, "branch", "--show-current")
	return strings.TrimSpace(string(out))
}

// ObjectsKiB is the size of the object store, loose and packed.
func (r Repo) ObjectsKiB(ctx context.Context) int64 {
	out, err := r.git(ctx, "count-objects", "-v")
	if err != nil {
		return 0
	}
	var n int64
	for _, l := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(l, ": ")
		if k == "size" || k == "size-pack" {
			kib, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			n += kib
		}
	}
	return n
}
