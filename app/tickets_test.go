package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/plugin"
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
  done = function(key, reply) calls[#calls + 1] = "done " .. key; reply({ moved = true, state = "Done" }) end,
  link = function(key, pr, done) calls[#calls + 1] = "link " .. key .. " #" .. math.floor(pr.number) .. " " .. pr.title; done() end }
`

// loadJira runs the Jira-like tracker as an installed plugin's Lua.
func loadJira(t *testing.T, s *errScene) *script.VM {
	t.Helper()
	m, err := plugin.ParseManifest([]byte(jiraManifest))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
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
	s.until(t, "Linear's tickets", func() bool { return strings.Contains(s.frameText(), "ENG-2231") })
	if text := s.frameText(); !strings.Contains(text, "Set your Jira API key") || strings.Contains(text, "OPS-7") {
		t.Errorf("Jira's tickets before its key:\n%s", text)
	}
	ctx := registry.WithAgent(context.Background(), assistantAgent)
	tools := registry.AgentToolsFor(s.reg, assistantAgent, true)
	my := tools[slices.IndexFunc(tools, func(t registry.AgentTool) bool { return t.Name == "my_tickets" })]
	out, err := s.callTool(t, ctx, my, nil)
	if err != nil || !strings.Contains(out, "ENG-2231") || !strings.Contains(out, "No Jira API key is set yet: :TicketKey jira sets one.") {
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
		return strings.Contains(text, "OPS-12") && strings.Contains(text, "ENG-2219")
	})
	text := strings.ReplaceAll(s.frameText(), "\n", "")
	order := []string{"OPS-7 Rotate the staging certificates", "ENG-2231 Search API returns stale results after a reindex",
		"ENG-2219 Round minor units half-even, per currency", "OPS-12 Alert on queue depth"}
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
	if want := "Jira OPS-7,Linear ENG-2231,Linear ENG-2240,Linear ENG-2219,Jira OPS-12,Linear ENG-2252"; strings.Join(got, ",") != want {
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
	lw := newLocalHost(t)
	if _, err := lw.w.AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { lw.connect(t)(cfg); change(cfg) })
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
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
		if p.Name == "search-api" {
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
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat: alert on queue depth", Head: se.Snapshot().Branch, Base: "main"})
	s.run("ship.refresh", nil)
	link := "link OPS-12 #" + itoa(pr.Number) + " feat: alert on queue depth"
	s.until(t, "the PR linked and the issue in review", func() bool {
		c := jiraCalls(jira)
		return slices.Contains(c, link) && slices.Contains(c, "review OPS-12")
	})
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "OPS-12 moved to In Review in Jira") })
}

// ticketScene is the app on a session host with the fake agent, Linear (its key set) and the Jira-like tracker loaded.
func ticketScene(t *testing.T) (*errScene, *localHost, *lineartest.Linear, *script.VM) {
	t.Helper()
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	secrets.SetSecret("JIRA_TOKEN", "jira-t0ken")
	lw := newLocalHost(t)
	if _, err := lw.w.AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { lw.connect(t)(cfg); change(cfg) })
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	loadLinear(t, s)
	jira := loadJira(t, s)
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	return s, lw, l, jira
}

// startTicketSession starts a session on ticket (none for ""), and gives it once it's in front.
func startTicketSession(t *testing.T, s *errScene, lw *localHost, prompt, ticket string) core.SessionState {
	t.Helper()
	before := s.store.State().Focus
	s.run("session.start", registry.Args{"kind": "plan", "prompt": prompt, "ticket": ticket, "provider": "fake"})
	st := waitState(t, s.store, prompt+" in front", func(st core.State) bool { return st.Focus != "" && st.Focus != before })
	lw.fake.WaitStarted(t)
	se, _ := st.Session(st.Focus)
	return se.Snapshot()
}

// continueTicket checks that Start work on key, from the omnibox's `!` and from the launcher's card, continues se
// rather than starting another session: each shows "Continue · <name>" first, and brings se to the front.
func continueTicket(t *testing.T, s *errScene, other core.SessionID, se core.SessionState, key, card string) {
	t.Helper()
	sessions := len(s.store.State().Sessions)
	name := se.Name
	if name == "" {
		name = string(se.ID)
	}
	focus := func() {
		if err := s.store.Apply(core.SessionFocused{ID: other}); err != nil {
			t.Fatal(err)
		}
	}

	focus()
	s.run("omnibox.open", registry.Args{"query": "!" + key})
	s.until(t, "the ticket's session", func() bool { return strings.Contains(s.frameText(), "Continue · "+name) })
	if !strings.Contains(s.frameText(), "↵ continues it in "+name) {
		t.Errorf("the preview doesn't say it continues:\n%s", s.frameText())
	}
	s.typeKeys("CR")
	waitState(t, s.store, key+"'s session in front", func(st core.State) bool { return st.Focus == se.ID })
	if strings.Contains(s.frameText(), "NEW SESSION") {
		t.Errorf("the launcher opened:\n%s", s.frameText())
	}

	focus()
	s.run("session.new", registry.Args{"kind": "plan", "ticket": key})
	s.until(t, "the card", func() bool {
		return strings.Contains(s.frameText(), card) && strings.Contains(s.frameText(), "Continue · "+name)
	})
	s.run("session.launch", nil)
	waitState(t, s.store, key+"'s session in front from the launcher", func(st core.State) bool { return st.Focus == se.ID })
	s.until(t, "the launcher closed", func() bool { return !strings.Contains(s.frameText(), "NEW SESSION") })
	if n := len(s.store.State().Sessions); n != sessions {
		t.Errorf("%d sessions, %d before: a duplicate was started", n, sessions)
	}
}

// "If a session already exists for a change we want to continue with it instead of creating a duplicate" (crit's
// Continue, crit-reference.md → Linear): Start work on a Jira-like ticket with a session brings that session to the
// front, from the omnibox and from the launcher, and an archived one is resumed.
func TestContinueTicketSession(t *testing.T) {
	s, lw, _, jira := ticketScene(t)
	se := startTicketSession(t, s, lw, "Queue depth alert", "OPS-12")
	other := startTicketSession(t, s, lw, "Something else", "")
	continueTicket(t, s, other.ID, se, "OPS-12", "Page when the jobs queue backs up.")

	s.run("session.archive", registry.Args{"id": string(se.ID)})
	waitState(t, s.store, "the session archived", func(st core.State) bool {
		a, _ := st.Session(se.ID)
		return a.Snapshot().Archived
	})
	if err := s.store.Apply(core.SessionFocused{ID: other.ID}); err != nil {
		t.Fatal(err)
	}
	sessions := len(s.store.State().Sessions)
	s.run("omnibox.open", registry.Args{"query": "!OPS-12"})
	s.until(t, "the archived session offered", func() bool { return strings.Contains(s.frameText(), "Continue · ") })
	s.typeKeys("CR")
	waitState(t, s.store, "the archived session resumed in front", func(st core.State) bool {
		a, _ := st.Session(se.ID)
		return st.Focus == se.ID && !a.Snapshot().Archived
	})
	if n := len(s.store.State().Sessions); n != sessions {
		t.Errorf("%d sessions, %d before", n, sessions)
	}
	if c := jiraCalls(jira); calledTimes(c, "start OPS-12") > 1 {
		t.Errorf("the ticket was started again: %v", c)
	}
}

// Continue is the app's, whatever the tracker: a Linear ticket whose session is stopped resumes it, and the launcher's
// card for it continues it too.
func TestLinearContinueTicketSession(t *testing.T) {
	s, lw, _, _ := ticketScene(t)
	se := startTicketSession(t, s, lw, "Stale results", "ENG-2231")
	other := startTicketSession(t, s, lw, "Something else", "")
	s.run("session.stop", registry.Args{"id": string(se.ID)})
	waitState(t, s.store, "the session stopped", func(st core.State) bool {
		a, _ := st.Session(se.ID)
		return !a.Snapshot().Agent.Running
	})
	continueTicket(t, s, other.ID, se, "ENG-2231", "linked PR #4398")
	waitState(t, s.store, "the session resumed", func(st core.State) bool {
		a, _ := st.Session(se.ID)
		return a.Snapshot().Agent.Running
	})
}

// shipPRs raises a PR for each of the session's projects named, on its branch, and gives them by project.
func shipPRs(t *testing.T, s *errScene, hub *ghtest.Server, se core.SessionState, projects ...string) map[string]gh.PR {
	t.Helper()
	out := map[string]gh.PR{}
	for _, name := range projects {
		branch := se.Branch
		for _, p := range se.Projects {
			if p.Name == name && p.Branch != "" {
				branch = p.Branch
			}
		}
		out[name] = hub.AddPR("acme/"+name, gh.PR{Title: "feat: " + name, Head: branch, Base: "main"})
	}
	s.run("ship.refresh", nil)
	return out
}

// "When the session's PR merges, its ticket moves to Done", through the tracker's Lua alone, whoever merged it: the
// Jira-like tracker's done is asked once the last of the session's open PRs is merged, not before.
func TestTicketDoneOnMerge(t *testing.T) {
	s, _, hub, first, _ := shipScene(t)
	jira := loadJira(t, s)
	var repos []string
	for _, p := range first.Projects {
		repos = append(repos, p.Repo)
	}
	s.run("session.start", registry.Args{"kind": "work", "prompt": "Queue depth alert", "ticket": "OPS-12", "provider": "fake", "repos": repos})
	st := waitState(t, s.store, "the OPS-12 session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Ticket == "OPS-12" && se.Snapshot().Branch != "" && len(se.Projects()) == 2
	})
	se, _ := st.Session(st.Focus)
	prs := shipPRs(t, s, hub, se.Snapshot(), "search-api", "web-dashboard")
	s.shipSettle(t, "both PRs in review", func() bool { return slices.Contains(jiraCalls(jira), "review OPS-12") })

	hub.Merge("acme/search-api", prs["search-api"].Number)
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the first PR read merged", func() bool {
		sh, ok := s.ship().ship(se.ID(), "search-api")
		return ok && sh.PR != nil && sh.PR.Merged
	})
	time.Sleep(50 * time.Millisecond)
	s.sh.runTodo()
	if slices.Contains(jiraCalls(jira), "done OPS-12") {
		t.Fatalf("done while web-dashboard's PR is open: %v", jiraCalls(jira))
	}

	hub.Merge("acme/web-dashboard", prs["web-dashboard"].Number)
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the ticket done", func() bool { return slices.Contains(jiraCalls(jira), "done OPS-12") })
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "OPS-12 moved to Done in Jira") })

	s.run("ship.refresh", nil)
	s.shipSettle(t, "a second read", func() bool { return true })
	time.Sleep(50 * time.Millisecond)
	s.sh.runTodo()
	if c := jiraCalls(jira); calledTimes(c, "done OPS-12") != 1 {
		t.Errorf("done twice: %v", c)
	}
}

// Linear's done on merge is the same tracker move, through its Lua and process: the ticket goes to its team's
// completed state.
func TestLinearDoneOnMerge(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s, _, hub, se, _ := shipScene(t, change)
	loadLinear(t, s)
	s.until(t, "the session's start moved", func() bool { return strings.Contains(s.fullText(), "ENG-2231 moved to In Progress in Linear") })
	prs := shipPRs(t, s, hub, se, "search-api")
	s.shipSettle(t, "the PR in review", func() bool { return strings.Contains(s.fullText(), "ENG-2231 moved to In Review in Linear") })
	hub.Merge("acme/search-api", prs["search-api"].Number)
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the ticket done", func() bool {
		for _, m := range l.Mutations() {
			in, _ := m.Vars["input"].(map[string]any)
			if m.Op == "issueUpdate" && m.Vars["id"] == "a3f9e2c1-0d44-4b7e-8f61-2c5d9b7e2231" && in["stateId"] == "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a05" {
				return true
			}
		}
		return false
	})
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "ENG-2231 moved to Done in Linear") })
}

func calledTimes(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}

// page.md → The selected ticket: "Plan" opens the launcher with the ticket's card, and Start gives the agent its
// context; a ticket with a session shows "Continue · <name>" in place of Plan, which brings that session to the front.
func TestTicketsPagePlanAndContinue(t *testing.T) {
	s, lw, _, _ := ticketScene(t)
	s.run("tickets.open", registry.Args{"tracker": "linear"})
	s.until(t, "the tickets", func() bool { return strings.Contains(s.frameText(), "ENG-2231") })
	s.clickText(t, "Search API returns stale results after a reindex")
	s.click(centre(s.within("Linear tickets page", "Plan").Bounds))
	s.frame()
	s.until(t, "the card", func() bool { return strings.Contains(s.frameText(), "linked PR #4398") })
	for _, want := range []string{"NEW SESSION", "from ticket", "ENG-2231", "Start Plan"} {
		if !strings.Contains(s.frameText(), want) {
			t.Errorf("the launcher lacks %q:\n%s", want, s.frameText())
		}
	}
	s.run("session.launch", nil)
	sent := lw.fake.WaitStarted(t).WaitSent(t)
	if !strings.Contains(sent.Text, "Linear ticket ENG-2231: Search API returns stale results after a reindex") {
		t.Errorf("the agent wasn't given the ticket:\n%s", sent.Text)
	}
	st := waitState(t, s.store, "the session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Ticket == "ENG-2231"
	})
	se, _ := st.Session(st.Focus)
	other := startTicketSession(t, s, lw, "Something else.", "")

	sessions := len(s.store.State().Sessions)
	s.run("Tickets", registry.Args{"args": "linear"})
	label := continueLabel(se)
	s.until(t, "Continue", func() bool { return strings.Contains(s.frameText(), label) })
	s.clickText(t, "Search API returns stale results after a reindex")
	s.click(centre(s.within("Linear tickets page", label).Bounds))
	s.frame()
	waitState(t, s.store, "the ticket's session in front", func(st core.State) bool { return st.Focus == se.ID() && st.Focus != other.ID })
	if n := len(s.store.State().Sessions); n != sessions {
		t.Errorf("%d sessions, %d before", n, sessions)
	}
	if s.tickets().page.open {
		t.Error("the page stayed open over the session")
	}
}

// AGENTS.md → "Any closable surface gets both an Escape path and a visible close": the tickets page closes with
// Escape and its ✕, opened alone, over Settings and over the omnibox.
func TestTicketsPageCloses(t *testing.T) {
	s, _, _, _ := ticketScene(t)
	x := s.tickets()
	for _, over := range []string{"", "Settings", "omnibox"} {
		for _, how := range []string{"Esc", "✕"} {
			switch over {
			case "Settings":
				s.run("Settings", nil)
			case "omnibox":
				s.run("omnibox.open", nil)
			}
			s.frame()
			s.run("tickets.open", nil)
			s.until(t, "the page", func() bool { return strings.Contains(s.frameText(), "OPS-7") })
			close := func() {
				if how == "Esc" {
					s.typeKeys("Esc")
				} else {
					s.click(centre(s.within("Tickets page", "✕").Bounds))
				}
				s.frame()
			}
			close()
			if over == "omnibox" {
				// The omnibox is over the page: it goes first, then the page.
				if s.omni().open || !x.page.open {
					t.Errorf("%s over the omnibox: omnibox open %v, page open %v", how, s.omni().open, x.page.open)
				}
				close()
			}
			if x.page.open {
				t.Errorf("opened over %q, %s didn't close it", over, how)
				x.page.open = false
			}
		}
	}
}

// page.md → Opening it: with no tracker the page is "Tickets", every tracker's beside each other in one order; with
// one it's that tracker's alone. The filter matches key, title and project.
func TestTicketsPageEveryTracker(t *testing.T) {
	s, _, _, _ := ticketScene(t)
	s.run("tickets.open", nil)
	s.until(t, "both trackers' tickets", func() bool {
		text := s.frameText()
		return strings.Contains(text, "OPS-12") && strings.Contains(text, "ENG-2219")
	})
	text := strings.ReplaceAll(s.frameText(), "\n", " ")
	if !strings.Contains(text, "TICKETS") || strings.Contains(text, "LINEAR TICKETS") || !strings.Contains(text, "6 assigned") {
		t.Errorf("the page's head:\n%s", text)
	}
	order := []string{"OPS-7", "ENG-2231", "ENG-2240", "ENG-2219", "OPS-12", "ENG-2252"}
	at := -1
	for _, want := range order {
		i := strings.Index(text, want)
		if i < at {
			t.Errorf("%q isn't in order:\n%s", want, text)
		}
		at = i
	}
	s.keyCommand(t, "/")
	for _, r := range "platform" {
		s.typeKeys(string(r))
	}
	text = s.frameText()
	if !strings.Contains(text, "OPS-7") || !strings.Contains(text, "OPS-12") || strings.Contains(text, "ENG-2231") {
		t.Errorf("filtered by project:\n%s", text)
	}
	s.typeKeys("Esc")
	s.run("tickets.open", registry.Args{"tracker": "jira"})
	s.frame()
	text = s.frameText()
	if !strings.Contains(text, "JIRA TICKETS") || strings.Contains(text, "ENG-2231") || !strings.Contains(text, "OPS-7") {
		t.Errorf("Jira's alone:\n%s", text)
	}
}

func (s *errScene) tickets() *ticketsPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*ticketsPlugin); ok {
			return x
		}
	}
	return nil
}
