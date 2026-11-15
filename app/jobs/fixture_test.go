package jobs_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/jobs"
	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and runs the sandbox's helper
// when the sandbox starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}

func jobsOf(s *apptest.Scene) *jobs.Jobs { return apptest.PluginOf[*jobs.Jobs](s, "jobs") }

// settleJobs frames until the Jobs page, what else the app is working on (a console's models) and the stats it
// shows have been read.
func settleJobs(s *apptest.Scene, x *jobs.Jobs) {
	s.T.Helper()
	s.Until("the jobs", func() bool { return !x.Busy() })
	s.Quiet()
	s.SettleStats()
}
