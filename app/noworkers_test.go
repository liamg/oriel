package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// PLAN.md, "No worker in any UI": every screen shows only this machine, and nothing offers a choice of worker or
// names one. Nothing the user can read says "worker": commands, options, contributions, help, the assistant's tools
// and prompt, and the main screens as drawn.
func TestNoWorkerInAnyUI(t *testing.T) {
	s, lw := hostScene(t)
	if _, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "rounding", Prompt: "Round half-even",
		Provider: "fake", Repos: []core.RepoID{core.RepoID(lw.repo)}}); err != nil {
		t.Fatal(err)
	}
	waitState(t, s.store, "the session", func(st core.State) bool { _, ok := st.Session("rounding"); return ok })
	check := func(where, text string) {
		t.Helper()
		if strings.Contains(strings.ToLower(text), "worker") {
			t.Errorf("%s says worker: %q", where, text)
		}
	}

	for _, c := range s.reg.Commands() {
		check("command "+c.ID, c.ID+" "+c.Title)
	}
	for _, k := range s.reg.Keymaps() {
		check("keymap "+k.Mode+" "+k.Keys, k.Mode+" "+k.Command+" "+k.Desc)
	}
	for _, o := range s.reg.Options().Specs() {
		check("option "+o.Name, o.Name+" "+o.Section+" "+o.Help+" "+strings.Join(o.Values, " "))
	}
	for _, c := range s.reg.Contributions("rail.entry") {
		e := c.(registry.RailEntry)
		check("rail entry "+e.Name, e.Name+" "+e.Title+" "+e.Command)
	}
	for _, c := range s.reg.Contributions("start.entry") {
		e := c.(registry.StartEntry)
		check("start entry "+e.Name, e.Name+" "+e.Title+" "+e.Desc+" "+e.Command)
	}
	for _, c := range s.reg.Contributions("hud.indicator") {
		e := c.(registry.Indicator)
		note := ""
		if e.Note != nil {
			note = e.Note()
		}
		check("indicator "+e.Name, e.Name+" "+e.Title+" "+e.Help+" "+note)
	}
	for _, c := range s.reg.Contributions("agent.tool") {
		e := c.(registry.AgentTool)
		check("agent tool "+e.Name, fmt.Sprint(e.Name, " ", e.Desc, " ", e.Params, " ", e.Reads))
	}
	check("the assistant's prompt", s.assistant().prompt())
	for _, p := range s.help().index().Pages() {
		check("help page "+p.ID, fmt.Sprint(p.ID, p.Title, p.Covers, p.Related))
		for _, b := range p.Blocks {
			check("help page "+p.ID, b.Anchor+" "+b.Text+" "+strings.Join(b.Items, " "))
		}
	}

	s.frame()
	s.frame()
	check("the start view", s.frameText())
	s.run("session.new", nil)
	for deadline := time.Now().Add(10 * time.Second); s.sh.cfg.Worker != nil; time.Sleep(10 * time.Millisecond) {
		s.frame()
		s.sh.runTodo()
		if strings.Contains(s.frameText(), "Pre-flight") && !strings.Contains(s.frameText(), "reading") || time.Now().After(deadline) {
			break
		}
	}
	check("the launcher", s.fullText())
	s.typeKeys("<Esc>")
	s.frame()
	s.run("stats.open", nil)
	s.settleStats(s.sh.stats)
	check("the Stats page", s.fullText())
	s.run("usage.open", nil)
	s.settleStats(s.sh.stats)
	check("the Usage page", s.fullText())
	s.run("jobs.new", nil)
	s.frame()
	s.frame()
	check("the New job dialog", s.fullText())
	s.jobs().form = nil
	s.run("jobs.open", nil)
	s.settleJobs(t, s.jobs())
	check("the Jobs page", s.fullText())
	s.jobs().open = false
	s.openMission(t)
	check("mission control", s.fullText())
	s.mission().close()
	s.run("hud.toggle", nil)
	s.frame()
	s.frame()
	check("the HUD", s.fullText())
}
