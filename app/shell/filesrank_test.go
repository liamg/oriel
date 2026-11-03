package shell_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/gitx/gittest"
)

// The files picker ranks files changed on the branch above others that match as well, and marks
// them with their status letter.
func TestFilesRankChangedFirst(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a/same.go": "package a\n", "b/same.go": "package b\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(map[string]string{"b/same.go": "package b\n\nvar X = 1\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.WaitGit()
	src := mustSource(t, s, "files")
	g, ok := shell.RankSource(s.Headless, src, "same", shell.PickerMax)
	if !ok || len(g.Items()) != 2 {
		t.Fatalf("hits: %+v", g)
	}
	if first := g.Items()[0]; first.Label != "b/same.go" || !strings.Contains(first.Note, "M") {
		t.Errorf("first %q (note %q), want the changed b/same.go marked M", first.Label, first.Note)
	}
}
