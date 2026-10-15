package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// jiraManifest and jiraLua are a second tracker, Jira-like, for tests only: its keys are OPS-n, and it answers from
// memory, keeping what it was asked to do in `calls`.
const jiraManifest = `id = "acme/jira"
version = "0.1.0"
description = "A Jira-like tracker"

[permissions]
secrets        = { JIRA_TOKEN = "acme.atlassian.net", why = "Your Jira API token" }
"agent.prompt" = { why = "Your Jira issues, to the agents" }
`

const jiraLua = `
local issues = {
  { key = "OPS-7", title = "Rotate the staging certificates", url = "https://acme.atlassian.net/browse/OPS-7", state = "In Progress",
    stage = "started", project = "Platform", priority = 1, priority_label = "Highest" },
  { key = "OPS-12", title = "Alert on queue depth", url = "https://acme.atlassian.net/browse/OPS-12", state = "To Do",
    project = "Platform", priority = 3, priority_label = "Medium", blocked_by = { "OPS-7" } },
}
calls = {}
oriel.ticket.tracker{ name = "jira", title = "Jira", order = 5, keys = "^OPS-[0-9]+$", secret = "JIRA_TOKEN",
  assigned = function(done) done(issues) end,
  lookup = function(key, done)
    for _, t in ipairs(issues) do
      if t.key == key then
        local d = {}
        for k, v in pairs(t) do d[k] = v end
        d.summary, d.facts = "Page when the jobs queue backs up.", { "acme.atlassian.net/browse/" .. key, "1 comment" }
        d.context = "Jira issue " .. key .. ": " .. t.title .. "\n\n## Comments (1)\n\n> Use the p95, not the max."
        return done(d)
      end
    end
    done(nil, "Issue does not exist: " .. key)
  end,
  start = function(key, done) calls[#calls + 1] = "start " .. key; done({ moved = true, state = "In Progress" }) end,
  review = function(key, done) calls[#calls + 1] = "review " .. key; done({ moved = true, state = "In Review" }) end,
  link = function(key, pr, done) calls[#calls + 1] = "link " .. key .. " #" .. math.floor(pr.number) .. " " .. pr.title; done() end }
`

// loadJira runs the Jira-like tracker as an installed plugin's Lua.
func loadJira(t *testing.T, s *errScene) *script.VM {
	t.Helper()
	m, err := ext.ParseManifest([]byte(jiraManifest))
	if err != nil {
		t.Fatal(err)
	}
	g := ext.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, ext.Plugin{ID: m.ID, Dir: t.TempDir(), Guard: g})
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(jiraLua)}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	return vm
}

func jiraCalls(vm *script.VM) []string {
	var out []string
	list, _ := global(vm, "calls").([]any)
	for _, c := range list {
		out = append(out, c.(string))
	}
	return out
}

// Tickets are the app's, built on any tracker: the omnibox's `!` lists Linear's and the Jira-like one's in one order
// (crit's: priority, blocked last, started, with a PR, key), asks for the key of a tracker that has none, and the
// assistant's my_tickets reads them all.
func TestTicketTrackersSideBySide(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s := newSceneWith(t, "", "", mockupSize, change)
	loadLinear(t, s)
	loadJira(t, s)

	s.run("omnibox.open", registry.Args{"query": "!"})
	s.until(t, "Linear's tickets", func() bool { return strings.Contains(s.frameText(), "INF-2231") })
	if text := s.frameText(); !strings.Contains(text, "Set your Jira API key") || strings.Contains(text, "OPS-7") {
		t.Errorf("Jira's tickets before its key:\n%s", text)
	}
	ctx := registry.WithAgent(context.Background(), assistantAgent)
	tools := registry.AgentToolsFor(s.reg, assistantAgent, true)
	my := tools[slices.IndexFunc(tools, func(t registry.AgentTool) bool { return t.Name == "my_tickets" })]
	out, err := s.callTool(t, ctx, my, nil)
	if err != nil || !strings.Contains(out, "INF-2231") || !strings.Contains(out, "No Jira API key is set yet: :TicketKey jira sets one.") {
		t.Errorf("my_tickets without Jira's key: %s %v", out, err)
	}

	s.run("omnibox.open", registry.Args{"query": "!"})
	s.until(t, "the ask for Jira's key", func() bool { return strings.Contains(s.frameText(), "Set your Jira API key") })
	s.typeKeys("CR")
	s.until(t, "the secrets dialog", func() bool { return strings.Contains(s.frameText(), "Set JIRA_TOKEN") })
	for _, r := range "jira-t0ken" {
		s.typeKeys(string(r))
	}
	s.typeKeys("CR")
	if v, _ := secrets.Secret("JIRA_TOKEN"); v != "jira-t0ken" {
		t.Fatalf("the Jira key: %q", v)
	}

	s.run("omnibox.open", registry.Args{"query": "!"})
	s.until(t, "both trackers' tickets", func() bool {
		text := s.frameText()
		return strings.Contains(text, "OPS-12") && strings.Contains(text, "INF-2219")
	})
	text := strings.ReplaceAll(s.frameText(), "\n", "")
	order := []string{"OPS-7 Rotate the staging certificates", "INF-2231 Usage API returns stale prices after plan change",
		"INF-2219 Round minor units half-even, per currency", "OPS-12 Alert on queue depth"}
	at := -1
	for _, want := range order {
		i := strings.Index(text, want)
		if i < at {
			t.Errorf("%q isn't in order:\n%s", want, text)
		}
		at = i
	}
	for _, want := range []string{"IN PROGRESS", "TO DO", "blocked · medium", "highest"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tickets lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Set your") {
		t.Errorf("a key is still asked for:\n%s", text)
	}

	out, err = s.callTool(t, ctx, my, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mine []myTicket
	if err := json.Unmarshal([]byte(out), &mine); err != nil {
		t.Fatalf("my_tickets: %v\n%s", err, out)
	}
	var got []string
	for _, m := range mine {
		got = append(got, m.Tracker+" "+m.Key)
	}
	if want := "Jira OPS-7,Linear INF-2231,Linear INF-2240,Linear INF-2219,Jira OPS-12,Linear INF-2252"; strings.Join(got, ",") != want {
		t.Errorf("my_tickets: %v, want %s", got, want)
	}
	if mine[4].URL != "https://acme.atlassian.net/browse/OPS-12" || !mine[4].Blocked || mine[4].Priority != "Medium" {
		t.Errorf("OPS-12: %+v", mine[4])
	}
}

// ui.md 04 and sessions.md: a ticket from any tracker starts work. The launcher's card is the Jira-like tracker's
// (its key pattern takes OPS-12, Linear's doesn't get it), the agent gets its context, and as the session starts that
// tracker, not Linear, moves it into progress.
func TestStartWorkFromAnyTracker(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	secrets.SetSecret("JIRA_TOKEN", "jira-t0ken")
	lw := newLocalWorker(t)
	if _, err := lw.client(t).AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { lw.connect(t)(cfg); change(cfg) })
	waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	loadLinear(t, s)
	jira := loadJira(t, s)
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}

	s.run("omnibox.open", registry.Args{"query": "!OPS-12"})
	s.until(t, "the ticket", func() bool { return strings.Contains(s.frameText(), "Alert on queue depth") })
	s.typeKeys("CR")
	s.until(t, "the card", func() bool { return strings.Contains(s.frameText(), "Page when the jobs queue backs up.") })
	for _, want := range []string{"from ticket", "OPS-12", "TO DO", "1 comment", "the ticket, comments and attachments go to the agent as context"} {
		if !strings.Contains(s.frameText(), want) {
			t.Errorf("the launcher lacks %q:\n%s", want, s.frameText())
		}
	}
	s.run("session.launch", nil)
	sent := lw.fake.WaitStarted(t).WaitSent(t)
	if !strings.Contains(sent.Text, "Jira issue OPS-12: Alert on queue depth") || !strings.Contains(sent.Text, "> Use the p95, not the max.") {
		t.Errorf("the agent wasn't given the issue:\n%s", sent.Text)
	}
	s.until(t, "the move", func() bool { return slices.Contains(jiraCalls(jira), "start OPS-12") })
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "OPS-12 moved to In Progress in Jira") })
	for _, m := range l.Mutations() {
		t.Errorf("Linear was changed: %+v", m)
	}
}

// A session's PR goes to its ticket's tracker, whichever: linked, then the ticket into review while it's open.
func TestPRLinkedToAnyTracker(t *testing.T) {
	s, _, hub, first, _ := shipScene(t)
	jira := loadJira(t, s)
	var api string
	for _, p := range first.Projects {
		if p.Name == "usage-api" {
			api = p.Repo
		}
	}
	s.run("session.start", registry.Args{"kind": "work", "prompt": "Queue depth alert", "ticket": "OPS-12", "provider": "fake",
		"repos": []string{api}})
	st := waitState(t, s.store, "the OPS-12 session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Ticket == "OPS-12" && se.Snapshot().Branch != ""
	})
	se, _ := st.Session(st.Focus)
	pr := hub.AddPR("acme/usage-api", gh.PR{Title: "feat: alert on queue depth", Head: se.Snapshot().Branch, Base: "main"})
	s.run("ship.refresh", nil)
	link := "link OPS-12 #" + itoa(pr.Number) + " feat: alert on queue depth"
	s.until(t, "the PR linked and the issue in review", func() bool {
		c := jiraCalls(jira)
		return slices.Contains(c, link) && slices.Contains(c, "review OPS-12")
	})
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "OPS-12 moved to In Review in Jira") })
}
