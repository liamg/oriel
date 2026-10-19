// Package gittest builds fixture repos by script, for tests of anything that reads git.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Repo is a fixture work tree in a temporary directory, with git's global and system config out of the way.
type Repo struct {
	T    testing.TB
	Dir  string
	When time.Time // the next commit's time; each commit moves it on a minute
}

// New is an empty repo on branch main.
func New(t testing.TB) *Repo {
	t.Helper()
	r := &Repo{T: t, Dir: t.TempDir(), When: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	r.Git("init", "-q", "-b", "main")
	return r
}

// Env is the environment git runs in for fixtures, with an identity for commits a command makes itself (a merge).
func Env() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
}

// Isolate keeps git's global and system config out of every git the test runs, the code under test's included,
// and gives it a fixed identity.
func Isolate(t testing.TB) {
	t.Helper()
	for _, kv := range [][2]string{{"GIT_CONFIG_GLOBAL", "/dev/null"}, {"GIT_CONFIG_SYSTEM", "/dev/null"},
		{"GIT_AUTHOR_NAME", "Fixture"}, {"GIT_AUTHOR_EMAIL", "fixture@example.com"},
		{"GIT_COMMITTER_NAME", "Fixture"}, {"GIT_COMMITTER_EMAIL", "fixture@example.com"}} {
		t.Setenv(kv[0], kv[1])
	}
}

// Git runs git in the repo and returns its output; a failure fails the test.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	return r.GitEnv(nil, args...)
}

func (r *Repo) GitEnv(env []string, args ...string) string {
	r.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(Env(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Write writes files, by path relative to the repo, making their directories.
func (r *Repo) Write(files map[string]string) {
	r.T.Helper()
	for name, body := range files {
		p := filepath.Join(r.Dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			r.T.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			r.T.Fatal(err)
		}
	}
}

// Remove deletes files from the work tree.
func (r *Repo) Remove(names ...string) {
	r.T.Helper()
	for _, n := range names {
		if err := os.Remove(filepath.Join(r.Dir, n)); err != nil {
			r.T.Fatal(err)
		}
	}
}

// Commit stages everything and commits it as who.
func (r *Repo) Commit(who, msg string) {
	r.T.Helper()
	r.Git("add", "-A")
	r.CommitStaged(who, msg)
}

// CommitStaged commits what's staged as who.
func (r *Repo) CommitStaged(who, msg string) {
	r.T.Helper()
	d := r.When.Format(time.RFC3339)
	r.When = r.When.Add(time.Minute)
	r.GitEnv([]string{"GIT_AUTHOR_NAME=" + who, "GIT_AUTHOR_EMAIL=" + strings.ToLower(who) + "@example.com", "GIT_COMMITTER_NAME=" + who,
		"GIT_COMMITTER_EMAIL=" + strings.ToLower(who) + "@example.com", "GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-qm", msg)
}

// Lines is n numbered lines, "line 1\n" on.
func Lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// CountGit puts a git on PATH that notes each run's arguments before running the real one, and returns the
// count so far of runs naming any of subcommands, or of every run without any.
func CountGit(t testing.TB) func(subcommands ...string) int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho \" $* \" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(subcommands ...string) int {
		b, _ := os.ReadFile(log)
		n := 0
		for _, l := range strings.Split(string(b), "\n") {
			if l == "" {
				continue
			}
			named := len(subcommands) == 0
			for _, c := range subcommands {
				named = named || strings.Contains(l, " "+c+" ")
			}
			if named {
				n++
			}
		}
		return n
	}
}
