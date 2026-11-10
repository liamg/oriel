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
)

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

// The branch diff of many changed files diffs only the file open in it.
func TestDiffViewDiffsOnlyTheOpenFile(t *testing.T) {
	s, check := diffViewScene(t, rig.ChangedRepo(t, 400), 400)
	s.Quiet()
	s.Frames(10)
	check()
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
