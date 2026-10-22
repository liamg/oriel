package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repo is a git work tree, read by running git in it. Env is added to git's environment: a sandboxed workspace's
// guard (sandbox.GitEnv). Cache, if set, keeps what's read between calls.
type Repo struct {
	Dir   string
	Env   []string
	Cache *Cache
}

// IsRepo reports whether dir is the top of a work tree.
func IsRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func (r Repo) git(ctx context.Context, args ...string) ([]byte, error) {
	return r.gitEnv(ctx, nil, args...)
}

// gitEnv is git with more environment. A git that finds the index locked by another, briefly, is run again.
func (r Repo) gitEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	for wait := 20 * time.Millisecond; ; wait *= 2 {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Dir, "--no-pager"}, args...)...)
		cmd.Env = append(append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), r.Env...), env...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		if !indexLocked(stderr.String()) || wait > lockWait {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// lockWait is about how long a git waits for another to let go of the index.
const lockWait = 2 * time.Second

// indexLocked says git failed because another git holds the index.
func indexLocked(stderr string) bool {
	return strings.Contains(stderr, "index.lock") && strings.Contains(stderr, "File exists")
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

// AheadBehind is the checked-out branch's Tracking.
func (r Repo) AheadBehind(ctx context.Context) (Tracking, error) {
	return r.BranchTracking(ctx, "HEAD")
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

// CommitStaged commits what's staged with message, adding each trailer ("Unreviewed-Agent-Lines: 38") as git commit
// --trailer does, and returns the new commit's hash. It commits as the repo's git identity, signed if git signs.
func (r Repo) CommitStaged(ctx context.Context, message string, trailers ...string) (string, error) {
	return r.commit(ctx, message, trailers, nil)
}

// commit commits what's staged (or, with paths, only those paths) with message and its trailers, and returns the
// new commit's hash.
func (r Repo) commit(ctx context.Context, message string, trailers, paths []string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("a commit needs a message")
	}
	args := []string{"-C", r.Dir, "commit", "-q", "-F", "-"}
	for _, t := range trailers {
		args = append(args, "--trailer", t)
	}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), r.Env...)
	cmd.Stdin = strings.NewReader(message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := r.git(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// StagedFiles are the paths with staged changes, as git names them.
func (r Repo) StagedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	var ps []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
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

// Root is the top of the work tree dir is in, and whether it's in one.
func Root(ctx context.Context, dir string) (string, bool) {
	out, err := Repo{Dir: dir}.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return filepath.FromSlash(strings.TrimSpace(string(out))), true
}
