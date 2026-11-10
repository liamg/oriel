package explain_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/gitx/gittest"
)

// The Changed units panel follows a changed file rewritten on disk, even when its status reads the same.
func TestChangedUnitsFollowTheDisk(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.go": "package p\n\nfunc A() {}\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"a.go": "package p\n\nfunc B() {}\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	s.WaitGit()
	s.Run("explain.units", nil)
	s.ExplainSettle()
	s.WantFrame("func B")
	path := filepath.Join(r.Dir, "a.go")
	if err := os.WriteFile(path, []byte("package p\n\nfunc C() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	s.Until("the panel to show the file as rewritten", func() bool {
		apptest.PluginOf[*explain.Plugin](s, "explain").Wait()
		return strings.Contains(s.Text(), "func C")
	})
}

// Changed units outside a repo says so, not that it's reading.
func TestChangedUnitsOutsideARepo(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("explain.units", nil)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	s.WantFrame("Not in a git repository")
}
