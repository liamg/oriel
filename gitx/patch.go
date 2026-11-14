package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ForkPoint is the abbreviated commit the branch left base at, their merge base (BaseRef's when base is ""), else
// HEAD's when there's no base to compare with; "" in a repo with no commits. MergeBaseOf is any two commits', with
// git's error.
func (r Repo) ForkPoint(ctx context.Context, base string) string {
	if base == "" {
		branch, _ := r.Branch(ctx)
		base = r.BaseRef(ctx, branch)
	}
	rev := "HEAD"
	if base != "" {
		if out, err := r.git(ctx, "merge-base", "HEAD", base); err == nil {
			rev = strings.TrimSpace(string(out))
		}
	}
	out, err := r.git(ctx, "rev-parse", "--short", rev)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Snapshot writes the work tree as it is, untracked files included and ignored ones not, as a tree object, and
// returns its hash. The index is left alone.
func (r Repo) Snapshot(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "oriel-index-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if b, err := os.ReadFile(filepath.Clean(strings.TrimSpace(string(out)))); err == nil {
		if err := os.WriteFile(tmp.Name(), b, 0o600); err != nil {
			return "", err
		}
	} else {
		os.Remove(tmp.Name())
	}
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	if _, err := r.gitEnv(ctx, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := r.gitEnv(ctx, env, "write-tree")
	return strings.TrimSpace(string(tree)), err
}

// Diff is the change from one commit or tree to another as git diff writes it.
func (r Repo) Diff(ctx context.Context, from, to string) (string, error) {
	out, err := r.git(ctx, "diff", "--no-color", "--no-ext-diff", "-M", from, to, "--")
	return string(out), err
}
