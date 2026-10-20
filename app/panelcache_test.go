package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
)

// The Changed units panel follows a changed file rewritten on disk, even when its status reads the same.
func TestChangedUnitsFollowTheDisk(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.go": "package p\n\nfunc A() {}\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"a.go": "package p\n\nfunc B() {}\n"})
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.run("explain.units", nil)
	s.explainSettle()
	s.wantText(t, "the panel", "func B")
	path := filepath.Join(r.Dir, "a.go")
	if err := os.WriteFile(path, []byte("package p\n\nfunc C() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	shows := func() bool {
		s.explainer().wait()
		s.sh.runTodo()
		return strings.Contains(s.frameText(), "func C")
	}
	if !s.Until(10*time.Second, shows) {
		t.Errorf("the panel never showed the file as rewritten:\n%s", s.frameText())
	}
}

// A record written while the log panel shows is listed in it.
func TestLogPanelShowsNewRecords(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.run("Log", nil)
	s.frame()
	s.frame()
	s.sh.cfg.Logs.Logger("core").Info("written while it shows")
	s.frame()
	s.wantText(t, "the log panel", "written while it shows")
}
