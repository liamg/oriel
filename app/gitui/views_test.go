package gitui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/tools/perf"
)

// changedExplorer is the Explorer open on dir's Changed scope, once its git is read.
func changedExplorer(t *testing.T, dir string, n int) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Quiet()
	s.Press("<M-1>")
	if !s.Headless.Until(30*time.Second, gitOf(s).Settled) {
		t.Fatalf("the git of %d changed files never settled", n)
	}
	return s
}

// The Explorer's frame doesn't grow with the files a branch changes.
func TestExplorerFrameWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	f := apptest.MeanFrames(changedExplorer(t, rig.ChangedRepo(t, 5), 5), changedExplorer(t, rig.ChangedRepo(t, 400), 400))
	few, many := f[0], f[1]
	t.Logf("a frame over 5 changed files: %v; over 400: %v", few, many)
	if many > 2*few+500*time.Microsecond {
		t.Errorf("400 changed files make the Explorer's frame %v, against %v over 5", many, few)
	}
}

// diffViewScene is the branch diff of dir's n changed files, once its first file shows; check fails the test if
// more than the open file were diffed.
func diffViewScene(t *testing.T, dir string, n int) (s *apptest.Scene, check func()) {
	s = apptest.New(t, apptest.Options{WorkDir: dir})
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": dir})
	made := func() (n int, done bool) {
		return diffOf(s).Made()
	}
	if !s.Headless.Until(30*time.Second, func() bool { _, done := made(); return done }) {
		t.Fatalf("the diff of %d changed files never showed its first", n)
	}
	return s, func() {
		if m, _ := made(); m > 1 {
			t.Errorf("%d of %d files diffed, with one open", m, n)
		}
	}
}

// The branch diff's frame doesn't grow with the files the branch changes: only the files open in it are diffed.
func TestDiffViewWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
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
	s.Quiet()
	s.Press("<M-2>")
	s.Headless.Until(5*time.Second, func() bool { return gitOf(s).State() != nil })
	s.Quiet()
	return s
}

// The Explorer's Uncommitted scope doesn't grow with the files changed: its rows are built when the status changes.
func TestExplorerUncommittedFrameWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	f := apptest.MeanFrames(uncommittedScene(t, rig.ChangedRepo(t, 5)), uncommittedScene(t, rig.ChangedRepo(t, 4000)))
	few, many := f[0], f[1]
	t.Logf("Uncommitted's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > few+1500*time.Microsecond {
		t.Errorf("4000 uncommitted files make the Explorer's frame %v, against %v over 5", many, few)
	}
}

// The diff says it's reading the work tree while its status hasn't been read, and a read that
// fails shows why with Retry, rather than "Reading…" for good.
func TestDiffWhileTheStatusLoads(t *testing.T) {
	r := rig.DiffRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.Quiet()
	gitOf(s).Unread(r.Dir)
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("Reading " + filepath.Base(r.Dir) + "'s changes…")

	gitOf(s).Unread(filepath.Join(r.Dir, "gone"))
	gitOf(s).Reload()
	s.Quiet()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("Retry")
	if strings.Contains(s.Text(), "'s changes…") {
		t.Error("still reading after the read failed")
	}
}

// A diff of a directory outside any repo says so once its root has been looked for.
func TestDiffOutsideARepo(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	dir := t.TempDir()
	s.Run("view.open", registry.Args{"id": gitui.DiffView, "instance": dir, "place": "tab"})
	s.Until("the root looked for", func() bool { s.Frame(); return gitOf(s).Settled() })
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame(filepath.Base(dir) + " isn't in a git repository")
}
