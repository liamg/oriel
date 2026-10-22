package sessions_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
)

// While loading, the job form gates on its agents. Save waits, saying why, until they've been read; ⌘↵ says the
// same; then the logged-in one is picked.
func TestJobFormWaitsForAgents(t *testing.T) {
	jr := rig.NewJobs(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(jr.W)})
	s.Now = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	hold := make(chan struct{})
	jr.Claude.Hold = hold
	s.Run("jobs.new", registry.Args{"prompt": "Sweep stale deps"})
	s.Frame()
	if n := strings.Count(s.Text(), "Looking for agents…"); n != 2 {
		t.Errorf("before the delay, %d lines say Looking for agents…: want only Save's and Save and run's", n)
	}
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if n := strings.Count(s.Text(), "Looking for agents…"); n != 3 {
		t.Errorf("after the delay, %d lines say Looking for agents…: want the agent section's too", n)
	}
	x := jobsOf(s)
	x.Submit(true)
	s.Frame()
	s.WantFrame("To save: still loading.")
	if len(jr.W.Jobs()) != 0 {
		t.Error("a job was saved before its agents were read")
	}

	close(hold)
	s.Until("the agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if _, agent, _ := x.Form(); agent != "claude" {
		t.Errorf("the agent picked once they arrived: %q", agent)
	}
	s.WantFrame("Claude", "Save and run")
}

// With no agent installed the form says so and names what a run uses; Save goes.
func TestJobFormNoAgents(t *testing.T) {
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: rig.NopExec{}, Load: rig.SteadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(w)})
	s.Run("jobs.new", nil)
	x := jobsOf(s)
	s.Until("the agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.Frame()
	s.WantFrame("No agents installed. Each run uses the first agent installed on this machine when it runs.")
	if strings.Contains(s.Text(), "Looking for agents") {
		t.Error("Save still waits with no agents to wait for")
	}
}
