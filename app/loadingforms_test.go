package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui/widget"
)

// ui.md, "Loading": the job form gates on its agents. Save waits, saying why, until they've been read; ⌘↵ says the
// same; then the logged-in one is picked.
func TestJobFormWaitsForAgents(t *testing.T) {
	jr := newJobsRig(t)
	s := newSceneWith(t, "", "", mockupSize, jr.connect(t))
	s.now = at(9, 0, 0)
	hold := make(chan struct{})
	jr.claude.Hold = hold
	s.run("jobs.new", registry.Args{"prompt": "Sweep stale deps"})
	s.frame()
	if n := strings.Count(s.frameText(), "Looking for agents…"); n != 2 {
		t.Errorf("before the delay, %d lines say Looking for agents…: want only Save's and Save and run's", n)
	}
	s.now = s.now.Add(time.Second)
	s.frame()
	if n := strings.Count(s.frameText(), "Looking for agents…"); n != 3 {
		t.Errorf("after the delay, %d lines say Looking for agents…: want the agent section's too", n)
	}
	x := s.jobs()
	x.submit(true)
	s.frame()
	s.wantFrame(t, "To save: still loading.")
	if len(jr.w.Jobs()) != 0 {
		t.Error("a job was saved before its agents were read")
	}

	close(hold)
	s.until(t, "the agents", func() bool { return x.form.agents.State().Phase == widget.Ready })
	s.now = s.now.Add(time.Second)
	s.frame()
	if x.form.agent != "claude" {
		t.Errorf("the agent picked once they arrived: %q", x.form.agent)
	}
	s.wantFrame(t, "Claude", "Save and run")
}

// With no agent installed the form says so and names what a run uses; Save goes.
func TestJobFormNoAgents(t *testing.T) {
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: nopExec{}, Load: steadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.Host, cfg.Store = w, w.Store() })
	s.run("jobs.new", nil)
	x := s.jobs()
	s.until(t, "the agents", func() bool { return x.form.agents.State().Phase == widget.Ready })
	s.frame()
	s.wantFrame(t, "No agents installed. Each run uses the first agent installed on this machine when it runs.")
	if strings.Contains(s.frameText(), "Looking for agents") {
		t.Error("Save still waits with no agents to wait for")
	}
}

// Start work's model menus are optional: while they're read the dialog says what Start uses meanwhile, and Start
// still goes.
func TestStartWorkModelsOptional(t *testing.T) {
	r, a, lw := planSession(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.waitText("Ready: every")
	lw.fake.Hold = make(chan struct{})
	r.clickText("Start work")
	r.waitText("Reading models…")
	r.waitText("Will use the session's model if you go on now")
	close(lw.fake.Hold)
	r.waitGone("Reading models…")
	r.typeKeys("<D-CR>")
	r.waitGone("START WORK")
}

const slowTracker = `pending = nil
asked = 0
oriel.ticket.tracker{ name = "slow", title = "Slow", keys = "^SLO-[0-9]+$",
  lookup = function(key, done) done(nil, "no " .. key) end,
  assigned = function(done) asked = asked + 1; pending = done end }`

// The tickets page shows skeleton rows while its tracker first answers, says nothing's assigned only once it has,
// and a refresh keeps the list up with Refresh busy.
func TestTicketsPageLoading(t *testing.T) {
	s := luaScene(t, slowTracker)
	s.now = at(9, 0, 0)
	vm := s.lua().vm
	s.run("tickets.open", registry.Args{"tracker": "slow"})
	s.frame()
	s.until(t, "the ask", func() bool { return global(vm, "asked") == float64(1) })
	if strings.Contains(s.frameText(), "Nothing assigned") {
		t.Error("the empty message showed before the tracker answered")
	}
	s.now = s.now.Add(time.Second)
	s.frame()
	if n := s.access("Tickets"); n.Role != "status" {
		t.Errorf("the skeleton reads as %+v", n)
	}
	answer := func() {
		t.Helper()
		if _, err := vm.Eval(script.Chunk{Name: "t", Source: []byte("pending({})")}); err != nil {
			t.Fatal(err)
		}
	}
	answer()
	s.now = s.now.Add(time.Second)
	s.until(t, "the answer", func() bool { return strings.Contains(s.frameText(), "Nothing assigned to you in Slow") })

	s.keyCommand(t, "r")
	s.until(t, "the second ask", func() bool { return global(vm, "asked") == float64(2) })
	s.frame()
	s.wantFrame(t, "Refreshing…", "Nothing assigned to you in Slow")
	answer()
	s.until(t, "the refresh", func() bool { return !strings.Contains(s.frameText(), "Refreshing…") })
}
