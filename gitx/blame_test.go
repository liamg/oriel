package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// Blame names each line's commit and author kind: lines you committed, an agent's (its name, or a Co-Authored-By
// trailer naming one), someone else's, and lines not committed yet, from a buffer's text rather than the file's.
// ref: oracle git
func TestBlameKinds(t *testing.T) {
	r := gittest.New(t)
	r.Git("config", "user.email", "ada@example.com")
	r.Write(map[string]string{"f.txt": "one\n"})
	r.Commit("Ada", "mine")
	r.Write(map[string]string{"f.txt": "one\ntwo\n"})
	r.Commit("Bob", "bob's")
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\n"})
	r.Commit("Ada", "paired\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\nfour\n"})
	r.Commit("Codex", "the agent's own")
	buffer := "one\ntwo\nthree\nfour\nfive, unsaved\n"
	bl, err := Repo{Dir: r.Dir}.Blame(context.Background(), "f.txt", []byte(buffer))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range bl {
		got = append(got, l.Kind.String()+" "+l.Author+" "+l.Summary)
	}
	want := []string{"you Ada mine", "other Bob bob's", "agent Ada paired", "agent Codex the agent's own", "uncommitted  "}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(bl[0].Hash) != 40 || bl[0].When.IsZero() || bl[4].Hash != "" {
		t.Errorf("first %+v, last %+v", bl[0], bl[4])
	}
}

// RV-2: agents.md, "it's recorded in a commit trailer (Unreviewed-Agent-Lines: 38)": CommitStaged commits what's
// staged and adds the trailer as git commit --trailer does; git reads it back as a trailer.
// ref: oracle git
func TestCommitStagedWithTrailer(t *testing.T) {
	r := gittest.New(t)
	r.Git("config", "user.name", "Ada")
	r.Git("config", "user.email", "ada@example.com")
	r.Write(map[string]string{"a.go": "package a\n", "b.go": "package a\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"a.go": "package a\n\nvar x = 1\n", "b.go": "package a\n\nvar y = 2\n"})
	r.Git("add", "a.go")
	repo := Repo{Dir: r.Dir}
	staged, err := repo.StagedFiles(context.Background())
	if err != nil || len(staged) != 1 || staged[0] != "a.go" {
		t.Fatalf("staged: %v %v", staged, err)
	}
	hash, err := repo.CommitStaged(context.Background(), "feat: x\n\nAdds x.", "Unreviewed-Agent-Lines: 38")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(r.Git("rev-parse", "HEAD")); got != hash {
		t.Errorf("returned %s, HEAD is %s", hash, got)
	}
	if got := strings.TrimSpace(r.Git("log", "-1", "--format=%(trailers:key=Unreviewed-Agent-Lines,valueonly)")); got != "38" {
		t.Errorf("the trailer reads %q", got)
	}
	if got := strings.TrimSpace(r.Git("show", "--name-only", "--format=", "HEAD")); got != "a.go" {
		t.Errorf("committed %q, want only what was staged", got)
	}
	if _, err := repo.CommitStaged(context.Background(), "  "); err == nil {
		t.Error("committed with no message")
	}
}

// Blame runs one git per file: who you are and the commits' trailers are read once, not for every file.
func TestBlameRunsOneGitPerFile(t *testing.T) {
	r := gittest.New(t)
	r.Git("config", "user.email", "ada@example.com")
	r.Write(map[string]string{"a.txt": "one\n", "b.txt": "two\n"})
	r.Commit("Ada", "both\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	runs := countGit(t)
	repo := Repo{Dir: r.Dir}
	if _, err := repo.Blame(context.Background(), "a.txt", []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	before := runs()
	bl, err := repo.Blame(context.Background(), "b.txt", []byte("two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n := runs() - before; n != 1 {
		t.Errorf("the second file ran %d gits, want 1", n)
	}
	if len(bl) != 1 || bl[0].Kind != ByAgent {
		t.Errorf("blame from what was read once: %+v", bl)
	}
}

// countGit puts a git on PATH that counts its runs before running the real one.
func countGit(t *testing.T) func() int {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho x >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(log)
		return bytes.Count(b, []byte("\n"))
	}
}
