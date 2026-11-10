//go:build perf

package gitui_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
)

// changedExplorer is the Explorer open on dir's Changed scope, once its git is read.
func changedExplorer(t *testing.T, dir string, n int) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	gitOf(s).Wait()
	s.Press("<M-1>")
	if !s.Headless.Until(30*time.Second, gitOf(s).Settled) {
		t.Fatalf("the git of %d changed files never settled", n)
	}
	return s
}

// The Explorer's frame doesn't grow with the files a branch changes.
func TestExplorerFrameWithManyChangesBudget(t *testing.T) {
	f := apptest.MeanFrames(changedExplorer(t, rig.ChangedRepo(t, 5), 5), changedExplorer(t, rig.ChangedRepo(t, 400), 400))
	few, many := f[0], f[1]
	t.Logf("a frame over 5 changed files: %v; over 400: %v", few, many)
	if many > 2*few+500*time.Microsecond {
		t.Errorf("400 changed files make the Explorer's frame %v, against %v over 5", many, few)
	}
}

// The branch diff's frame doesn't grow with the files the branch changes: only the files open in it are diffed.
func TestDiffViewWithManyChangesBudget(t *testing.T) {
	a, checkA := diffViewScene(t, rig.ChangedRepo(t, 5), 5)
	b, checkB := diffViewScene(t, rig.ChangedRepo(t, 4000), 4000)
	f := apptest.MeanFrames(a, b)
	few, many := f[0], f[1]
	checkA()
	checkB()
	t.Logf("the diff's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("4000 changed files make the diff's frame %v, against %v over 5", many, few)
	}
}

// uncommittedScene is the Explorer open on dir's Uncommitted scope.
func uncommittedScene(t *testing.T, dir string) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	gitOf(s).Wait()
	s.Press("<M-2>")
	s.Headless.Until(5*time.Second, func() bool { return gitOf(s).State() != nil })
	gitOf(s).Wait()
	return s
}

// The Explorer's Uncommitted scope doesn't grow with the files changed: its rows are built when the status changes.
func TestExplorerUncommittedFrameWithManyChangesBudget(t *testing.T) {
	f := apptest.MeanFrames(uncommittedScene(t, rig.ChangedRepo(t, 5)), uncommittedScene(t, rig.ChangedRepo(t, 4000)))
	few, many := f[0], f[1]
	t.Logf("Uncommitted's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > few+1500*time.Microsecond {
		t.Errorf("4000 uncommitted files make the Explorer's frame %v, against %v over 5", many, few)
	}
}
