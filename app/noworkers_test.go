package app_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/shell/help"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// No worker in any UI: every screen shows only this machine, and nothing offers a choice of worker or
// names one. Nothing the user can read says "worker": commands, options, contributions, help, the assistant's tools
// and prompt, and the main screens as drawn.
func TestNoWorkerInAnyUI(t *testing.T) {
	s, lw := apptest.HostScene(t)
	if _, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "rounding", Prompt: "Round half-even",
		Provider: "fake", Repos: []core.RepoID{core.RepoID(lw.Repo)}}); err != nil {
		t.Fatal(err)
	}
	s.UntilState("the session", func(st core.State) bool { _, ok := st.Session("rounding"); return ok })
	check := func(where, text string) {
		t.Helper()
		text = strings.ReplaceAll(text, filepath.Dir(lw.Repo), "") // the test's own directory, as the launcher lists the repo
		if strings.Contains(strings.ToLower(text), "worker") {
			t.Errorf("%s says worker: %q", where, text)
		}
	}

	for _, c := range s.Registry().Commands() {
		check("command "+c.ID, c.ID+" "+c.Title)
	}
	for _, k := range s.Registry().Keymaps() {
		check("keymap "+k.Mode+" "+k.Keys, k.Mode+" "+k.Command+" "+k.Desc)
	}
	for _, o := range s.Registry().Options().Specs() {
		check("option "+o.Name, o.Name+" "+o.Section+" "+o.Help+" "+strings.Join(o.Values, " "))
	}
	for _, c := range s.Registry().Contributions("rail.entry") {
		e := c.(uireg.RailEntry)
		check("rail entry "+e.Name, e.Name+" "+e.Title+" "+e.Command)
	}
	for _, c := range s.Registry().Contributions("start.entry") {
		e := c.(uireg.StartEntry)
		check("start entry "+e.Name, e.Name+" "+e.Title+" "+e.Desc+" "+e.Command)
	}
	for _, c := range s.Registry().Contributions("hud.indicator") {
		e := c.(uireg.Indicator)
		note := ""
		if e.Note != nil {
			note = e.Note()
		}
		check("indicator "+e.Name, e.Name+" "+e.Title+" "+e.Help+" "+note)
	}
	for _, c := range s.Registry().Contributions("agent.tool") {
		e := c.(registry.AgentTool)
		check("agent tool "+e.Name, fmt.Sprint(e.Name, " ", e.Desc, " ", e.Params, " ", e.Reads))
	}
	check("the assistant's prompt", apptest.PluginOf[*agentui.Assistant](s, "assistant").Prompt())
	var srcs []help.Source
	hs, _ := s.Registry().HelpSources()
	for _, h := range hs {
		srcs = append(srcs, help.Source{Name: h.Source, FS: h.FS})
	}
	ix, _ := help.Load(srcs)
	for _, p := range ix.Pages() {
		check("help page "+p.ID, fmt.Sprint(p.ID, p.Title, p.Covers, p.Related))
		for _, b := range p.Blocks {
			check("help page "+p.ID, b.Anchor+" "+b.Text+" "+strings.Join(b.Items, " "))
		}
	}

	s.Frame()
	s.Frame()
	check("the start view", s.Text())
	s.Run("session.new", nil)
	s.Quiet() // what the launcher reads is in
	check("the launcher", s.FullText())
	s.Type("<Esc>")
	s.Frame()
	s.Run("stats.open", nil)
	s.SettleStats()
	check("the Stats page", s.FullText())
	s.Run("usage.open", nil)
	s.SettleStats()
	check("the Usage page", s.FullText())
	s.Run("jobs.new", nil)
	s.Frame()
	s.Frame()
	check("the New job dialog", s.FullText())
	s.Type("Esc")
	s.Run("jobs.open", nil)
	s.Until("the Jobs page", func() bool { return strings.Contains(s.Text(), "JOBS") })
	s.SettleStats()
	check("the Jobs page", s.FullText())
	s.Close("jobs")
	s.Run("hud.toggle", nil)
	s.Frame()
	s.Frame()
	check("the HUD", s.FullText())
}
