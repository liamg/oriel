package sessions_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/sandbox/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and runs the sandbox's helper
// when the sandbox starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}

func launcherOf(s *apptest.Scene) *sessions.Launcher {
	return apptest.PluginOf[*sessions.Launcher](s, "launcher")
}
