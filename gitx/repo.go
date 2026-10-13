package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repo is a git work tree, read by running git in it.
type Repo struct{ Dir string }

// IsRepo reports whether dir is the top of a work tree.
func IsRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func (r Repo) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir, "--no-pager"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Files are the work tree's files that git doesn't ignore, tracked or not, sorted.
func (r Repo) Files(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// Branch is the branch checked out, or "HEAD" when it's detached.
func (r Repo) Branch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// Tracking is how far the branch is from its upstream; Upstream is "" when it has none.
type Tracking struct {
	Upstream      string
	Ahead, Behind int
}

func (r Repo) AheadBehind(ctx context.Context) (Tracking, error) {
	up, err := r.git(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return Tracking{}, nil
	}
	out, err := r.git(ctx, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return Tracking{}, err
	}
	var t Tracking
	t.Upstream = strings.TrimSpace(string(up))
	if f := strings.Fields(string(out)); len(f) == 2 {
		t.Ahead, _ = strconv.Atoi(f[0])
		t.Behind, _ = strconv.Atoi(f[1])
	}
	return t, nil
}

// CommitTimes are when each commit reachable from HEAD since then was made, newest first.
func (r Repo) CommitTimes(ctx context.Context, since time.Time) ([]time.Time, error) {
	out, err := r.git(ctx, "log", "--format=%ct", "--since="+strconv.FormatInt(since.Unix(), 10), "HEAD")
	if err != nil {
		return nil, err
	}
	var ts []time.Time
	for _, l := range strings.Fields(string(out)) {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil {
			ts = append(ts, time.Unix(n, 0))
		}
	}
	return ts, nil
}

// Commit is one commit, as a log line shows it.
type Commit struct {
	Hash, Author, Subject string
	When                  time.Time
}

// Last is HEAD's commit.
func (r Repo) Last(ctx context.Context) (Commit, error) {
	out, err := r.git(ctx, "log", "-1", "--format=%h%x00%an%x00%ct%x00%s", "HEAD")
	if err != nil {
		return Commit{}, err
	}
	f := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 4)
	if len(f) != 4 {
		return Commit{}, fmt.Errorf("git log: %q", out)
	}
	n, _ := strconv.ParseInt(f[2], 10, 64)
	return Commit{Hash: f[0], Author: f[1], When: time.Unix(n, 0), Subject: f[3]}, nil
}

// Author is someone who committed, and how many commits reachable from HEAD are theirs.
type Author struct {
	Name    string
	Commits int
}

// Authors are HEAD's authors, most commits first.
func (r Repo) Authors(ctx context.Context) ([]Author, error) {
	out, err := r.git(ctx, "shortlog", "-sn", "--no-merges", "HEAD")
	if err != nil {
		return nil, err
	}
	var as []Author
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		n, name, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok {
			continue
		}
		c, _ := strconv.Atoi(strings.TrimSpace(n))
		as = append(as, Author{Name: name, Commits: c})
	}
	return as, nil
}
