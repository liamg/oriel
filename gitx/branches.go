package gitx

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Branch is a local branch. Gone means its upstream is configured but no longer exists.
type Branch struct {
	Name, Upstream string
	Gone           bool
	Head           string
	When           time.Time
	Subject        string
}

// Branches are the local branches, by name.
func (r Repo) Branches(ctx context.Context) ([]Branch, error) {
	out, err := r.git(ctx, "for-each-ref", "refs/heads",
		"--format=%(refname:short)%00%(upstream:short)%00%(upstream:track)%00%(objectname)%00%(committerdate:unix)%00%(subject)")
	if err != nil {
		return nil, err
	}
	var bs []Branch
	for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(l, "\x00", 6)
		if len(f) != 6 {
			continue
		}
		n, _ := strconv.ParseInt(f[4], 10, 64)
		bs = append(bs, Branch{Name: f[0], Upstream: f[1], Gone: f[2] == "[gone]", Head: f[3], When: time.Unix(n, 0), Subject: f[5]})
	}
	slices.SortFunc(bs, func(a, b Branch) int { return strings.Compare(a.Name, b.Name) })
	return bs, nil
}

// MergedInto are the local branches whose tips ref already contains.
func (r Repo) MergedInto(ctx context.Context, ref string) ([]string, error) {
	out, err := r.git(ctx, "branch", "--merged", ref, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// BranchTracking is how far branch is from its upstream; Upstream is "" when it has none.
func (r Repo) BranchTracking(ctx context.Context, branch string) (Tracking, error) {
	up, err := r.git(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return Tracking{}, nil
	}
	out, err := r.git(ctx, "rev-list", "--left-right", "--count", branch+"..."+branch+"@{upstream}")
	if err != nil {
		return Tracking{}, err
	}
	t := Tracking{Upstream: strings.TrimSpace(string(up))}
	if f := strings.Fields(string(out)); len(f) == 2 {
		t.Ahead, _ = strconv.Atoi(f[0])
		t.Behind, _ = strconv.Atoi(f[1])
	}
	return t, nil
}

// DeleteBranch deletes a local branch; one not merged is refused unless force.
func (r Repo) DeleteBranch(ctx context.Context, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := r.git(ctx, "branch", flag, "--", name)
	return err
}

// FastForward brings branch up to its upstream, refusing anything but a fast-forward. Checked out here, it's
// pulled, and only into a clean work tree; otherwise origin's branch is fetched into it.
func (r Repo) FastForward(ctx context.Context, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	if cur, _ := r.Branch(ctx); cur == branch {
		if r.Dirty(ctx) {
			return fmt.Errorf("%s has uncommitted changes: commit or stash them first", branch)
		}
		if _, err := r.git(ctx, "pull", "--quiet", "--ff-only"); err != nil {
			return fmt.Errorf("fast-forwarding %s: %w", branch, err)
		}
		return nil
	}
	if _, err := r.git(ctx, "fetch", "origin", "refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("fast-forwarding %s: %w", branch, err)
	}
	return nil
}
