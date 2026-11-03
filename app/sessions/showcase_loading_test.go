//go:build showcase

package sessions_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// The launcher while its probe runs, once it's slow, failed, loaded, and with ⌘↵ pressed while it waits.
func TestShowcaseLoadingLauncher(t *testing.T) {
	fake := agenttest.NewFake()
	fake.Name, fake.Hold = "claude", make(chan struct{})
	lw := rig.NewHost(t, fake)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Light()
	s.Now = apptest.At(14, 30, 0)
	s.Run("session.new", registry.Args{"prompt": "Fix stale search results"})
	s.Now = s.Now.Add(time.Second)
	s.Shot("launcher-1-loading")
	s.Now = s.Now.Add(11 * time.Second)
	s.Shot("launcher-2-slow")
	s.Registry().Run(context.Background(), "session.launch", nil)
	s.Shot("launcher-5-cmd-enter-while-loading")
	close(fake.Hold)
	s.Until("the probe", func() bool { return strings.Contains(s.Text(), "Claude") })
	s.Now = s.Now.Add(time.Second)
	s.Shot("launcher-4-loaded")

	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: rig.NopExec{}, Load: rig.SteadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	f := apptest.New(t, apptest.Options{Config: apptest.Connect(w)})
	f.Light()
	f.Now = apptest.At(14, 30, 0)
	f.Run("session.new", registry.Args{"prompt": "notes"})
	f.Until("the failure", func() bool { return strings.Contains(f.Text(), "no agents are set up") })
	f.Now = f.Now.Add(time.Second)
	f.Shot("launcher-3-failed")
}

func TestShowcaseLoadingJobForm(t *testing.T) {
	jr := rig.NewJobs(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(jr.W)})
	s.Light()
	s.Now = apptest.At(9, 0, 0)
	jr.Claude.Hold = make(chan struct{})
	s.Run("jobs.new", registry.Args{"prompt": "Sweep stale deps"})
	s.Now = s.Now.Add(4 * time.Second)
	s.Shot("jobform-loading")
	close(jr.Claude.Hold)
}
