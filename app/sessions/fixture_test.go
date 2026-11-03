package sessions_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and runs the sandbox's helper
// when the sandbox starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}

func jobsOf(s *apptest.Scene) *sessions.Jobs { return apptest.PluginOf[*sessions.Jobs](s, "jobs") }
func launcherOf(s *apptest.Scene) *sessions.Launcher {
	return apptest.PluginOf[*sessions.Launcher](s, "launcher")
}
func reposOf(s *apptest.Scene) *sessions.Repos { return apptest.PluginOf[*sessions.Repos](s, "repos") }

// settleJobs frames until the Jobs page and the stats it shows have been read.
func settleJobs(s *apptest.Scene, x *sessions.Jobs) {
	s.T.Helper()
	s.Until("the jobs", func() bool { return !x.Busy() })
	s.SettleStats()
}
