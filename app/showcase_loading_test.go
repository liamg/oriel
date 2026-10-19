//go:build showcase

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

func (s *errScene) light(t *testing.T) {
	t.Helper()
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	r, err := theme.Resolve(st, theme.Light)
	if err != nil {
		t.Fatal(err)
	}
	s.ui.SetTheme(r)
	s.frame()
}

// The launcher while its probe runs, once it's slow, failed, loaded, and with ⌘↵ pressed while it waits.
func TestShowcaseLoadingLauncher(t *testing.T) {
	fake := agenttest.NewFake()
	fake.Name, fake.Hold = "claude", make(chan struct{})
	lw := newLocalHost(t, fake)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.light(t)
	s.now = at(14, 30, 0)
	s.run("session.new", registry.Args{"prompt": "Fix stale search results"})
	s.now = s.now.Add(time.Second)
	s.shot(t, "launcher-1-loading")
	s.now = s.now.Add(11 * time.Second)
	s.shot(t, "launcher-2-slow")
	s.reg.Run(context.Background(), "session.launch", nil)
	s.shot(t, "launcher-5-cmd-enter-while-loading")
	close(fake.Hold)
	s.until(t, "the probe", func() bool { return strings.Contains(s.frameText(), "Claude") })
	s.now = s.now.Add(time.Second)
	s.shot(t, "launcher-4-loaded")

	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: nopExec{}, Load: steadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	f := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.Host, cfg.Store = w, w.Store() })
	f.light(t)
	f.now = at(14, 30, 0)
	f.run("session.new", registry.Args{"prompt": "notes"})
	f.until(t, "the failure", func() bool { return strings.Contains(f.frameText(), "no agents are set up") })
	f.now = f.now.Add(time.Second)
	f.shot(t, "launcher-3-failed")
}

func TestShowcaseLoadingShip(t *testing.T) {
	s, se := mockup18(t)
	s.light(t)
	x := s.ship()
	x.mu.Lock()
	x.reports[se.ID] = &shipReport{loading: true, doing: map[string]string{}, failed: map[string]string{}}
	x.mu.Unlock()
	s.run("ship.open", nil)
	s.frame()
	s.now = s.now.Add(time.Second)
	s.shot(t, "ship-loading")
}

func TestShowcaseLoadingJobForm(t *testing.T) {
	jr := newJobsRig(t)
	s := newSceneWith(t, "", "", mockupSize, jr.connect(t))
	s.light(t)
	s.now = at(9, 0, 0)
	jr.claude.Hold = make(chan struct{})
	s.run("jobs.new", registry.Args{"prompt": "Sweep stale deps"})
	s.now = s.now.Add(4 * time.Second)
	s.shot(t, "jobform-loading")
	close(jr.claude.Hold)
}

func TestShowcaseLoadingStats(t *testing.T) {
	s, _, _ := statsScene(t)
	s.light(t)
	s.run("stats.open", nil)
	s.frame()
	s.now = s.now.Add(time.Second)
	s.shot(t, "stats-loading")
}
