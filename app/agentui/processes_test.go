package agentui_test

import (
	"context"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

var devServer = core.BackgroundTask{ID: "1001", Kind: "terminal", Description: "npm run dev", PID: 4242, CPU: 1.5, Memory: 2 << 20}

// What the last turn left running that won't wake the agent is listed with what's known of its use, and Stop stops
// it: the provider is asked, and the process leaves the list.
func TestStopAProcessTheTurnLeftRunning(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Has = &agent.Features{Terminal: true, Fork: true, StopProcesses: true}
	r, a, _ := apptest.AgentWindowWith(t, lw, apptest.WindowOptions{Plugins: []plugin.Plugin{registers{lw.Fake}}})
	r.TypeKeys("<D-A>")
	r.WaitText("SESSION STATS")
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{Running: []core.BackgroundTask{devServer, {ID: "1002", Kind: "terminal", Description: "go run ./watch"}}})
	r.WaitFrame("the running processes", func(s string) bool {
		return apptest.HasAll(s, "RUNNING", "2 processes", "npm run dev", "pid 4242 · 1.5% cpu · 2.0 MB", "go run ./watch", "Stop")
	})
	r.ClickText("Stop")
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(a.Stopped(), []string{"1001"}) {
		if time.Now().After(deadline) {
			t.Fatalf("the provider was asked to stop %v, want [1001]", a.Stopped())
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.WaitGone("npm run dev")
	r.WaitText("go run ./watch")
}

// A provider that can't stop its processes still shows them, with no Stop, and agent.process.stop refuses.
func TestNoStopWithoutTheFeature(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Has = &agent.Features{Terminal: true, Fork: true}
	r, a, _ := apptest.AgentWindowWith(t, lw, apptest.WindowOptions{Plugins: []plugin.Plugin{registers{lw.Fake}}})
	r.TypeKeys("<D-A>")
	r.WaitText("SESSION STATS")
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{Running: []core.BackgroundTask{devServer}})
	s := r.WaitText("npm run dev")
	if regexp.MustCompile(`\bStop\b`).MatchString(s) {
		t.Error("the console offers Stop for a provider that can't")
	}
	err := r.Do(func() error {
		return r.Host.Run(context.Background(), "agent.process.stop", registry.Args{"id": "1001"})
	})
	if err == nil || len(a.Stopped()) != 0 {
		t.Errorf("agent.process.stop = %v, stopped %v", err, a.Stopped())
	}
}
