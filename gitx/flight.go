package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Flight is a branch on its way to a pull request (ui.md 18): what it hasn't committed, how far its base has moved
// on, and what origin hasn't got.
type Flight struct {
	Branch      string
	Base        string      // the ref it's compared with: origin/main, else main
	Uncommitted []FileState // one per path: Working (staged and unstaged together, ± against HEAD) or Untracked
	Behind      int         // commits on Base that HEAD lacks
	Conflicts   []string    // files a rebase onto Base would conflict in
	Commits     []Commit    // HEAD's since Base, newest first
	Unpushed    []Commit    // of those, what origin's branch lacks: all of them before the first push
	Pushed      bool        // origin has the branch
}

// ErrConflict is a rebase that stopped on conflicts; it was undone.
var ErrConflict = errors.New("the rebase conflicts")

// FlightBase is the ref a branch based on base is compared with: origin's, when there is one.
func (r Repo) FlightBase(ctx context.Context, base string) string {
	base = strings.TrimPrefix(base, "origin/")
	if base == "" {
		br, _ := r.Branch(ctx)
		return r.BaseRef(ctx, br)
	}
	if r.verify(ctx, "refs/remotes/origin/"+base) {
		return "origin/" + base
	}
	if r.verify(ctx, "refs/heads/"+base) {
		return base
	}
	return ""
}

func (r Repo) verify(ctx context.Context, ref string) bool {
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// Flight reads the branch's way to a pull request against base ("main"; "" for origin's default).
func (r Repo) Flight(ctx context.Context, base string) (*Flight, error) {
	f := &Flight{}
	var err error
	if f.Branch, err = r.Branch(ctx); err != nil {
		return nil, err
	}
	st, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	counts, _ := r.numstat(ctx, "diff", "HEAD", "--numstat", "-z")
	for _, fs := range st.Files(Uncommitted) {
		if i := slices.IndexFunc(f.Uncommitted, func(o FileState) bool { return o.Path == fs.Path }); i >= 0 {
			if fs.Area == Staged {
				f.Uncommitted[i].Code = fs.Code
			}
			continue
		}
		if fs.Area != Untracked {
			fs.Add, fs.Del, fs.Area = 0, 0, Working
			counts.fill(&fs)
		}
		f.Uncommitted = append(f.Uncommitted, fs)
	}
	slices.SortFunc(f.Uncommitted, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	f.Base = r.FlightBase(ctx, base)
	if f.Base == "" || !r.verify(ctx, "HEAD") {
		return f, nil
	}
	if out, err := r.git(ctx, "rev-list", "--count", "HEAD.."+f.Base); err == nil {
		f.Behind, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	if f.Behind > 0 {
		f.Conflicts = r.conflicts(ctx, f.Base)
	}
	f.Commits, _ = r.log(ctx, f.Base+"..HEAD")
	f.Unpushed = f.Commits
	if f.Branch != "HEAD" && r.verify(ctx, "refs/remotes/origin/"+f.Branch) {
		f.Pushed = true
		f.Unpushed, _ = r.log(ctx, "origin/"+f.Branch+"..HEAD")
	}
	return f, nil
}

// log is the commits in a range, newest first.
func (r Repo) log(ctx context.Context, rng string) ([]Commit, error) {
	out, err := r.git(ctx, "log", "--format=%h%x00%an%x00%ct%x00%s", rng)
	if err != nil {
		return nil, err
	}
	var cs []Commit
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(l, "\x00", 4)
		if len(f) != 4 {
			continue
		}
		n, _ := strconv.ParseInt(f[2], 10, 64)
		cs = append(cs, Commit{Hash: f[0], Author: f[1], When: time.Unix(n, 0), Subject: f[3]})
	}
	return cs, nil
}

// conflicts are the files merging onto would conflict in, read without touching the work tree (git merge-tree).
func (r Repo) conflicts(ctx context.Context, onto string) []string {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "merge-tree", "--write-tree", "--name-only", "--no-messages", "HEAD", onto)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), r.Env...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return nil
	}
	return slices.Compact(lines[1:])
}

// Commit commits paths (every change when there are none) with message, as the user's git identity has it.
func (r Repo) Commit(ctx context.Context, message string, paths ...string) error {
	_, err := r.CommitWith(ctx, message, nil, paths...)
	return err
}

// CommitWith is Commit with trailers ("Unreviewed-Agent-Lines: 21"), and returns the new commit's hash.
func (r Repo) CommitWith(ctx context.Context, message string, trailers []string, paths ...string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("a commit needs a message")
	}
	add := []string{"add", "-A"}
	if len(paths) > 0 {
		add = append(append(add, "--"), paths...)
	}
	if _, err := r.git(ctx, add...); err != nil {
		return "", err
	}
	return r.commit(ctx, message, trailers, paths)
}

// Push pushes HEAD to origin's branch, and tracks it.
func (r Repo) Push(ctx context.Context, branch string) error {
	if branch == "" || branch == "HEAD" {
		return errors.New("nothing to push: no branch is checked out")
	}
	_, err := r.git(ctx, "push", "--quiet", "-u", "origin", "HEAD:refs/heads/"+branch)
	return err
}

// Fetch brings origin's branches up to date.
func (r Repo) Fetch(ctx context.Context) error {
	if !r.HasRemote(ctx, "origin") {
		return errors.New("no remote called origin")
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	_, err := r.git(ctx, "fetch", "--quiet", "origin")
	return err
}

// Rebase puts the branch's commits on top of onto, carrying uncommitted work over. One that conflicts is undone,
// and the error wraps ErrConflict, naming the files.
func (r Repo) Rebase(ctx context.Context, onto string) error {
	if _, err := r.git(ctx, "-c", "core.editor=true", "rebase", "--autostash", "--quiet", onto); err != nil {
		files, _ := r.git(ctx, "diff", "--name-only", "--diff-filter=U")
		_, _ = r.git(ctx, "rebase", "--abort")
		if names := strings.Fields(string(files)); len(names) > 0 {
			return fmt.Errorf("%w in %s", ErrConflict, strings.Join(names, ", "))
		}
		return err
	}
	return nil
}
