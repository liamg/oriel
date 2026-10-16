package gitx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// FileAt a commit's hash reads every file through one git: what it reads is what git cat-file blob gives, a file
// the commit lacks is not there, and a path with spaces reads as any other.
// ref: oracle git
func TestFileAtReadsThroughOneGit(t *testing.T) {
	r := gittest.New(t)
	files := map[string]string{"a b.txt": "spaced\n", "empty.txt": ""}
	for i := range 30 {
		files[fmt.Sprintf("d/f%02d.txt", i)] = strings.Repeat(fmt.Sprintf("line of %d\n", i), i)
	}
	r.Write(files)
	r.Commit("Ada", "base")
	hash := strings.TrimSpace(r.Git("rev-parse", "--short", "HEAD"))
	runs := gittest.CountGit(t)
	repo := Repo{Dir: r.Dir}
	for p := range files {
		got, ok := repo.FileAt(context.Background(), hash, p)
		if want := r.Git("cat-file", "blob", hash+":"+p); !ok || string(got) != want {
			t.Errorf("%s: %q %v, want %q", p, got, ok, want)
		}
	}
	if _, ok := repo.FileAt(context.Background(), hash, "nope.txt"); ok {
		t.Error("a file the commit lacks was there")
	}
	if got, ok := repo.FileAt(context.Background(), hash, "a b.txt"); !ok || string(got) != "spaced\n" {
		t.Errorf("after a missing file: %q %v", got, ok)
	}
	if n := runs() - len(files); n > 1 {
		t.Errorf("%d files read with %d gits", len(files)+2, n)
	}
}
