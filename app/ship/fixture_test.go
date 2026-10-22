package ship_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and runs the sandbox's helper
// when the sandbox starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}

var (
	git        = rig.Git
	writeFiles = rig.WriteFiles
	shipRepo   = rig.ShipRepo
)

func shipScene(t *testing.T, more ...func(*apptest.Config)) (s *apptest.Scene, lw *rig.Host, hub *ghtest.Server, se core.SessionState, ups map[string]string) {
	return apptest.ShipScene(t, more...)
}

func shipOf(s *apptest.Scene) *ship.Ship { return apptest.PluginOf[*ship.Ship](s, "ship") }

func shipSettle(s *apptest.Scene, t *testing.T, what string, ok func() bool) {
	t.Helper()
	s.ShipSettle(what, ok)
}

func shipIdle(s *apptest.Scene) { s.ShipIdle() }

func shipDir(se core.SessionState, name string) string { return apptest.ProjectDir(se, name) }

// framesUntil frames, calling check with each frame's number, until ok, failing after 15s.
func framesUntil(s *apptest.Scene, t *testing.T, what string, check func(frame int), ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for n := 0; ; n++ {
		s.RunQueued()
		s.Frame()
		check(n)
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s:\n%s", what, s.Text())
		}
	}
}

// editorFile is the file an editor view's instance shows.
func editorFile(s *apptest.Scene, instance string) (string, bool) {
	v, ok := s.Registry().ViewByID(kit.EditorView)
	if !ok {
		return "", false
	}
	l, ok := uireg.As[registry.Located](v)
	if !ok {
		return "", false
	}
	return l.Location(instance)
}

func githubCode(t *testing.T, r registry.Registry) *registry.CodeHost {
	t.Helper()
	h, ok := registry.HostByName(r, "github")
	if !ok {
		t.Fatal("GitHub isn't a code host")
	}
	return &h
}
