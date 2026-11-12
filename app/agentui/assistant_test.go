package agentui_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/voice/voicetest"
)

var asstNow = time.Date(2026, 10, 15, 14, 5, 0, 0, time.UTC)

func assistantOf(s *apptest.Scene) *agentui.Assistant {
	return apptest.PluginOf[*agentui.Assistant](s, "assistant")
}

// assistantScene is the app on a session host with the fake provider, at the mockup's size and a fixed clock, with
// a scripted microphone and a speaker that keeps what it's given.
func assistantScene(t *testing.T, o host.Options, mic *voicetest.Mic, tr *voicetest.Transcriber) (*apptest.Scene, *rig.Host, *saidSpeaker) {
	t.Helper()
	lw := rig.NewHostWith(t, o)
	sp := &saidSpeaker{}
	if mic == nil {
		mic, tr = &voicetest.Mic{}, &voicetest.Transcriber{}
	}
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		apptest.Connect(lw.W)(cfg)
		cfg.Voice = agentui.VoiceConfig{Mic: mic, Transcriber: tr, Speaker: sp}
	}})
	s.Now = asstNow
	assistantOf(s).SetNow(func() time.Time { return s.Now })
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Frame()
	return s, lw, sp
}

// asstCall runs one of the tools the assistant's agent was offered, as the agent would, off the loop; the result
// comes on the channel while the test frames.
func asstCall(a *agenttest.FakeSession, name string, args any) chan [2]any {
	done := make(chan [2]any, 1)
	go func() {
		out, err := a.CallErr(name, args)
		done <- [2]any{out, err}
	}()
	return done
}

// settle frames until the call answers.
func settleCall(t *testing.T, s *apptest.Scene, done chan [2]any) (string, error) {
	t.Helper()
	var r [2]any
	s.Until("the tool's answer", func() bool {
		select {
		case r = <-done:
			return true
		default:
			return false
		}
	})
	err, _ := r[1].(error)
	return r[0].(string), err
}

// started is the next agent the fake starts, framing while it waits: what starts it may wait on the loop.
func started(t *testing.T, s *apptest.Scene, lw *rig.Host) *agenttest.FakeSession {
	t.Helper()
	var a *agenttest.FakeSession
	s.Until("an agent to start", func() bool {
		select {
		case a = <-lw.Fake.Started:
			return true
		default:
			return false
		}
	})
	return a
}

func toolNames(a *agenttest.FakeSession) []string {
	var out []string
	for _, t := range a.Spec.Tools {
		out = append(out, t.Name)
	}
	return out
}

// The assistant as a drawer: "what needs me?" Answers come back as actionable rows (number keys pick one). It shows
// what it's allowed to do, and its data sources with how fresh each is. It's a resident utility session, never
// claude -p.
func TestAssistantWhatNeedsMe(t *testing.T) {
	s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
	s.Run("session.start", registry.Args{"kind": "work", "name": "rounding", "provider": "fake", "repos": []string{lw.Repo}})
	work := lw.Fake.WaitStarted(t)
	work.Ask(agent.Permission{ID: "p1", Tool: "Bash", Summary: "make e2e"})
	s.Run("session.start", registry.Args{"kind": "work", "name": "other", "provider": "fake", "repos": []string{lw.Repo}})
	lw.Fake.WaitStarted(t)
	st := s.UntilState("rounding waiting on a permission", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Status() == core.StatusPermission && st.Focus == "other"
	})

	// The window follows the session in front, out of the start view, which keeps to its own keys.
	other, _ := st.Session("other")
	s.Until("the window to follow the session", func() bool {
		s.Reopen()
		return s.Host().Here().Project == kit.SessionDir(other.Snapshot())
	})
	assistantOf(s).SetNow(func() time.Time { return s.Now })
	s.Frame()

	// ⌘/ floats its window over a page that's open, with the composer focused; esc minimises it, leaving the page.
	// Narrow, it's the conversation; dragged wider, what it can do and reads comes beside it.
	s.Run("settings.open", nil)
	s.Frame()
	s.Press("<D-/>")
	s.Frame()
	if !assistantOf(s).Open() {
		t.Fatal("⌘/ didn't open the assistant")
	}
	s.WantFrame("ORI", "New")
	if strings.Contains(s.Text(), "WHAT IT CAN DO") {
		t.Error("the narrow window has the side")
	}
	widenAssistant(t, s)
	s.WantFrame("ORI", "WHAT IT CAN DO", "SOURCES", "RECENT ACTIONS")
	s.Press("<D-/>")
	s.Press("<D-/>")
	s.Frame()
	s.Press("<Esc>")
	if assistantOf(s).Open() || !s.Shown("settings") {
		t.Fatalf("esc: assistant open %v, settings open %v", assistantOf(s).Open(), s.Shown("settings"))
	}
	s.Close("settings")
	s.Press("<D-/>")
	s.Frame()

	s.TypeInto("Ask the assistant", "what needs me?")
	s.Press("<CR>")
	ua := lw.Fake.WaitStarted(t)
	if ua.Spec.Pack.Skill != "assistant" || !strings.Contains(ua.Spec.Pack.Text, "Oriel's assistant") || ua.Spec.Policy != agent.PolicyRefuse {
		t.Errorf("the utility agent: %+v", ua.Spec.Pack)
	}
	if !regexp.MustCompile(`^\[\w{3} \d+ \w{3} \d\d:\d\d, typed\] what needs me\?$`).MatchString(ua.Spec.Prompt) {
		t.Errorf("its first turn: %q", ua.Spec.Prompt)
	}
	names := toolNames(ua)
	for _, want := range []string{"reply", "sessions", "past_sessions", "open_session", "start_session", "stop_session", "review_pr",
		"run_job", "my_prs", "review_requests", "settings"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s in %v", want, names)
		}
	}
	for _, not := range []string{"lsp_definition", "thread", "commit_draft"} {
		if slices.Contains(names, not) {
			t.Errorf("the assistant was offered %s, a session's tool", not)
		}
	}
	if slices.Contains(toolNames(work), "sessions") {
		t.Error("a work session's agent was offered the assistant's tools")
	}

	ua.Emit(agent.TurnStart{})
	ua.Emit(agent.ToolCall{ID: "c1", Tool: "mcp__oriel__sessions", Input: json.RawMessage(`{}`)})
	out, err := settleCall(t, s, asstCall(ua, "sessions", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var lines []agentui.SessionLine
	if json.Unmarshal([]byte(out), &lines) != nil || len(lines) != 2 || lines[0].ID != "rounding" || lines[0].Status != "permission" ||
		len(lines[0].Asks) != 1 || lines[0].Asks[0] != "Bash make e2e" || lines[0].Kind != "work" {
		t.Fatalf("sessions: %s", out)
	}
	if _, err := settleCall(t, s, asstCall(ua, "reply", map[string]any{"text": "One thing waits on you.", "items": []map[string]any{
		{"title": "rounding", "text": "wants to run make e2e", "meta": "1m · this machine", "session": "rounding"}}})); err != nil {
		t.Fatal(err)
	}
	ua.Emit(agent.TurnEnd{})
	// The reply comes before the turn ends: the answer, with its turn over.
	s.Until("the answer", func() bool {
		return strings.Contains(s.Text(), "One thing waits on you.") && !strings.Contains(s.Text(), "thinking…")
	})
	s.WantFrame("what needs me?", "✓ SESSIONS", "rounding", "wants to run make e2e", "1m · this machine", "⌘1",
		"start session", "asks first", "never", "sessions", "github", "web search", "now")
	if o := s.UI().Overflowing(); len(o) > 0 {
		t.Errorf("text drawn past its box:\n%s", strings.Join(o, "\n"))
	}
	s.Lint("the assistant's window")
	s.Golden("assistant-window")

	// ⌘1 picks the first row: its session comes to the front, and the window stays over it until it's minimised.
	s.Press("<D-1>")
	s.UntilState("rounding in front", func(st core.State) bool { return st.Focus == "rounding" })
	if !assistantOf(s).Open() {
		t.Error("the window went when its row opened a session")
	}
	// Its permission comes up as the session comes to the front; Escape puts it off.
	s.Frame()
	s.NodeRole(render.RoleDialog, "Permission: Bash(make e2e)")
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Type("Esc")
	s.Frame()

	// Minimised to Ori on the rail, what it says next is counted there until the window shows it.
	s.ClickNode("Minimise")
	if assistantOf(s).Open() {
		t.Fatal("– didn't minimise the window")
	}
	s.Run("assistant.say", registry.Args{"text": "anything else?"})
	ua.Emit(agent.TurnStart{})
	if _, err := settleCall(t, s, asstCall(ua, "reply", map[string]any{"text": "Nothing else."})); err != nil {
		t.Fatal(err)
	}
	ua.Emit(agent.TurnEnd{})
	oriBadge := func() *render.AccessNode { return accessFind(accessFind(s.Access(), "Ori"), "1") }
	s.Until("the unread count", func() bool { s.Frame(); return oriBadge() != nil })
	s.ClickNode("Ori")
	if !assistantOf(s).Open() {
		t.Fatal("clicking Ori didn't open the window")
	}
	s.WantFrame("Nothing else.")
	if oriBadge() != nil {
		t.Error("still unread with the window open")
	}
}

// Confirmation cards, and every action has an undo with a countdown. An act shows what it will do and waits; y does
// it, n doesn't, and what it made can be undone for a few seconds.
func TestAssistantAsksFirst(t *testing.T) {
	s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
	c := lw.W
	if _, err := c.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	s.Run("assistant.open", registry.Args{"text": "plan a cache for pricing"})
	s.Frame()
	widenAssistant(t, s)
	ua := lw.Fake.WaitStarted(t)

	done := asstCall(ua, "start_session", map[string]any{"repo": "pricing", "prompt": "Add a cache in front of the price lookups."})
	s.Until("the card", func() bool { return strings.Contains(s.Text(), "Start a plan session") })
	s.WantFrame("CONFIRM · START_SESSION", "The assistant wants to start a plan session in pricing.", "repository", "pricing — "+lw.Repo,
		"THE PROMPT THE AGENT WILL BE GIVEN", "Add a cache in front of the price lookups.", "A plan session drafts a plan first", "Yes", "No", "120s")
	if len(s.Store().State().Sessions) != 0 {
		t.Fatal("a session started before the yes")
	}
	s.Lint("a confirmation")
	s.Type("y")
	out, err := settleCall(t, s, done)
	if err != nil || !strings.Contains(out, `"session"`) {
		t.Fatalf("start_session: %q %v", out, err)
	}
	st := s.UntilState("the plan session", func(st core.State) bool { return len(st.Sessions) == 1 })
	planned := st.Sessions[0]
	if planned.Kind != core.KindPlan || planned.Prompt != "Add a cache in front of the price lookups." || st.Focus == planned.ID {
		t.Errorf("started %s %q, focus %q: it starts behind the session in front", planned.Kind, planned.Prompt, st.Focus)
	}
	s.Until("the action", func() bool { return strings.Contains(s.Text(), "✓ started a plan session in pricing") })
	s.WantFrame("you said yes", "Open", "Undo 10s", "RECENT ACTIONS")
	s.ClickTop("Undo 10s")
	s.Until("the session deleted", func() bool { return len(s.Store().State().Sessions) == 0 })
	s.Until("undone", func() bool { return strings.Contains(s.Text(), "undone: started a plan session") })

	// No: nothing starts, and the agent is told so.
	done = asstCall(ua, "review_pr", map[string]any{"pr": "acme/api#4410"})
	s.Until("the review's card", func() bool { return strings.Contains(s.Text(), "Start a review") })
	s.WantFrame("acme/api", "#4410", "https://github.com/acme/api/pull/4410")
	s.Type("n")
	if _, err := settleCall(t, s, done); err == nil || !strings.Contains(err.Error(), "said no") {
		t.Errorf("a no: %v", err)
	}
	s.Until("the no", func() bool { return strings.Contains(s.Text(), "you said no — nothing was done") })
	if len(s.Store().State().Sessions) != 0 {
		t.Error("a review started on a no")
	}
}

// my_tickets (the tickets'), builds and agentbus's request_review are their features' tools:
// a feature or plugin offers them for the assistant through the registry (this my_tickets shadows the app's), and they reach it, and only it, like its own. An act
// of theirs asks first just the same.
func TestPluginToolsReachTheAssistant(t *testing.T) {
	s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
	requested := ""
	for _, tool := range []registry.AgentTool{
		{Name: "my_tickets", For: []string{kit.AssistantAgent}, ReadOnly: true, Reads: "linear", Desc: "The user's Linear tickets.",
			Run: func(registry.Ctx, map[string]any) (any, error) {
				return []map[string]string{{"key": "ENG-2231", "title": "search api", "state": "In Progress"}}, nil
			}},
		{Name: "builds", For: []string{kit.AssistantAgent}, ReadOnly: true, Reads: "builds", Desc: "The builds running and how they went.",
			Run: func(registry.Ctx, map[string]any) (any, error) { return "2 PRs failing CI", nil }},
		{Name: "request_review", For: []string{kit.AssistantAgent}, Params: map[string]string{"pr": "string", "who": "string?"},
			Desc: "Ask someone on agentbus to review a pull request.",
			Confirm: func(_ registry.Ctx, p map[string]any) (registry.Confirmation, error) {
				return registry.Confirmation{Title: "Ask for a review", Lead: "The assistant wants to ask @" + p["who"].(string) + " to review " + p["pr"].(string),
					Line: "ask for a review"}, nil
			},
			Run: func(_ registry.Ctx, p map[string]any) (any, error) {
				requested = p["who"].(string)
				return registry.Acted{Line: "asked @" + requested + " to review acme/api#4421"}, nil
			}},
	} {
		tool.Source = "test"
		s.Registry().Contribute(tool)
	}
	s.Run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": []string{lw.Repo}})
	if names := toolNames(lw.Fake.WaitStarted(t)); slices.Contains(names, "my_tickets") || slices.Contains(names, "request_review") {
		t.Errorf("a session's agent was offered the assistant's: %v", names)
	}
	s.Run("assistant.open", registry.Args{"text": "my tickets?"})
	s.Frame()
	widenAssistant(t, s)
	ua := lw.Fake.WaitStarted(t)
	for _, want := range []string{"my_tickets", "builds", "request_review"} {
		if !slices.Contains(toolNames(ua), want) {
			t.Errorf("no %s for the assistant", want)
		}
	}
	ua.Emit(agent.ToolCall{ID: "c1", Tool: "mcp__oriel__my_tickets"})
	if out, err := settleCall(t, s, asstCall(ua, "my_tickets", map[string]any{})); err != nil || !strings.Contains(out, "ENG-2231") {
		t.Errorf("my_tickets: %q %v", out, err)
	}
	// The call, under recent actions: "linear" alone is in the sources list from the start.
	s.Until("the call", func() bool { return s.Has("✓ MY_TICKETS") })
	s.WantFrame("builds", "linear", "request review", "asks first")

	// With the drawer away, its card stacks over the window with the inbox's, and is answered there.
	s.Run("assistant.close", nil)
	done := asstCall(ua, "request_review", map[string]any{"pr": "acme/api#4421", "who": "maria"})
	s.Until("its card", func() bool { return strings.Contains(s.Text(), "Ask for a review") })
	if requested != "" || assistantOf(s).Open() {
		t.Fatal("it ran before the yes, or the drawer came down for it")
	}
	s.WantFrame("ASSISTANT · REQUEST_REVIEW", "The assistant wants to ask @maria to review acme/api#4421")
	s.ClickTop("Yes")
	if _, err := settleCall(t, s, done); err != nil || requested != "maria" {
		t.Errorf("request_review: %v, %q", err, requested)
	}
	s.Run("assistant.open", nil)
	s.Until("what it did", func() bool { return strings.Contains(s.Text(), "✓ asked @maria to review acme/api#4421") })
}

// PR lists, mine and review-requested: the assistant reads them from GitHub through the
// user's gh, and says which has a session on it.
func TestAssistantReadsPullRequests(t *testing.T) {
	hub := ghtest.New(t)
	hub.AddPR("acme/api", gh.PR{Title: "retry transient 502s", Author: "maria", Reviewers: []string{"you"}})
	mine := hub.AddPR("acme/api", gh.PR{Title: "round half to even", Draft: true})
	s, lw, _ := assistantScene(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }}, nil, nil)
	s.Run("assistant.say", registry.Args{"text": "my PRs?"})
	ua := lw.Fake.WaitStarted(t)
	out, err := settleCall(t, s, asstCall(ua, "my_prs", map[string]any{}))
	var prs []agentui.PRLine
	if err != nil || json.Unmarshal([]byte(out), &prs) != nil || len(prs) != 1 || prs[0].Title != "round half to even" || !prs[0].Draft ||
		prs[0].URL != mine.URL || prs[0].Repo != "acme/api" {
		t.Errorf("my_prs: %s %v", out, err)
	}
	out, err = settleCall(t, s, asstCall(ua, "review_requests", map[string]any{}))
	if err != nil || !strings.Contains(out, "retry transient 502s") || !strings.Contains(out, `"author": "maria"`) {
		t.Errorf("review_requests: %s %v", out, err)
	}
}

// A Lua agent tool says who it's for, so init.lua can give the assistant a source of its own.
func TestLuaToolsForTheAssistant(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.agent.tool{ name = "deploys", desc = "Today's deploys", ["for"] = { "assistant" }, read_only = true,
  reads = "deploys", run = function() return "2 today" end }`)
	tools := registry.AgentToolsFor(s.Registry(), kit.AssistantAgent, true)
	i := slices.IndexFunc(tools, func(t registry.AgentTool) bool { return t.Name == "deploys" })
	if i < 0 || !tools[i].ReadOnly || tools[i].Reads != "deploys" {
		t.Fatalf("the assistant's tools: %+v", tools)
	}
	if slices.ContainsFunc(registry.AgentToolsFor(s.Registry(), "work", false), func(t registry.AgentTool) bool { return t.Name == "deploys" }) {
		t.Error("a work session's agent was offered the assistant's tool")
	}
	out, err := s.CallTool(registry.WithAgent(context.Background(), kit.AssistantAgent), apptest.Timeout, tools[i], nil)
	if err != nil || out != "2 today" {
		t.Errorf("deploys: %q %v", out, err)
	}
}

// asstWorkScene is a work session on two projects, pricing and web, in front, and the assistant's agent started.
func asstWorkScene(t *testing.T) (*apptest.Scene, *agenttest.FakeSession, core.SessionState) {
	t.Helper()
	s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
	web := filepath.Join(t.TempDir(), "web")
	rig.Git(t, filepath.Dir(web), "init", "-q", "-b", "main", web)
	rig.WriteFiles(t, lw.Repo, map[string]string{"round.go": "package pricing\n\nfunc Round() {}\n"})
	rig.WriteFiles(t, web, map[string]string{"src/usage.ts": "export const usage = 1\n", "src/usage.test.ts": "test('usage', () => {})\n"})
	for _, dir := range []string{lw.Repo, web} {
		rig.Git(t, dir, "add", "-A")
		rig.Git(t, dir, "commit", "-q", "-m", "files")
	}
	s.Run("session.start", registry.Args{"kind": "work", "name": "rounding", "provider": "fake", "repos": []string{lw.Repo, web}})
	lw.Fake.WaitStarted(t)
	st := s.UntilState("the session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && len(se.Snapshot().Projects) == 2
	})
	s.Until("the window to follow the session", s.Reopen)
	assistantOf(s).SetNow(func() time.Time { return s.Now })
	s.Frame()
	se, _ := st.Session(st.Focus)
	s.Run("assistant.open", registry.Args{"text": "where's that test I was working on?"})
	return s, lw.Fake.WaitStarted(t), se.Snapshot()
}

func asstProject(se core.SessionState, name string) string {
	for _, p := range se.Projects {
		if p.Name == name {
			return p.Dir
		}
	}
	return ""
}

// The user asks "where's that test I was working on?": changed_files lists what the session changed, newest first,
// telling the tests (testrun.Set.TestFile); focus_file puts the likeliest in front at its line; where says what's
// there now.
func TestAssistantFindsTheTest(t *testing.T) {
	s, ua, se := asstWorkScene(t)
	pricing, web := asstProject(se, "pricing"), asstProject(se, "web")
	rig.WriteFiles(t, pricing, map[string]string{"round.go": "package pricing\n\nfunc Round() { _ = 1 }\n",
		"round_test.go": "package pricing\n\nimport \"testing\"\n\nfunc TestRound(t *testing.T) {}\n"})
	rig.WriteFiles(t, web, map[string]string{"src/usage.test.ts": "test('usage', () => { expect(1) })\n"})
	for path, ago := range map[string]time.Duration{filepath.Join(pricing, "round.go"): time.Hour,
		filepath.Join(web, "src/usage.test.ts"): 20 * time.Minute, filepath.Join(pricing, "round_test.go"): 2 * time.Minute} {
		at := time.Now().Add(-ago)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	out, err := settleCall(t, s, asstCall(ua, "changed_files", map[string]any{"tests": true}))
	if err != nil {
		t.Fatal(err)
	}
	var files []agentui.AsstFile
	if json.Unmarshal([]byte(out), &files) != nil || len(files) != 2 ||
		files[0].Project != "pricing" || files[0].Path != "round_test.go" || !files[0].Test || files[0].Ago != "2m" ||
		files[1].Project != "web" || files[1].Path != "src/usage.test.ts" || files[1].Area != "uncommitted" || files[1].Add != 1 {
		t.Fatalf("changed tests: %s", out)
	}
	out, err = settleCall(t, s, asstCall(ua, "changed_files", map[string]any{}))
	if err != nil || json.Unmarshal([]byte(out), &files) != nil || len(files) != 3 || files[2].Path != "round.go" || files[2].Test {
		t.Fatalf("changed files: %s %v", out, err)
	}

	out, err = settleCall(t, s, asstCall(ua, "focus_file", map[string]any{"project": "pricing", "path": "round_test.go", "line": 5}))
	if err != nil {
		t.Fatal(err)
	}
	s.Frame()
	if at := s.Host().Here(); at.File != filepath.Join(pricing, "round_test.go") || at.Line != 5 {
		t.Fatalf("focus_file %s: the editor is at %s:%d", out, at.File, at.Line)
	}
	if !assistantOf(s).Open() {
		t.Error("focus_file put the assistant away: it asks next whether that's the one")
	}

	out, err = settleCall(t, s, asstCall(ua, "where", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var w agentui.WhereLine
	if json.Unmarshal([]byte(out), &w) != nil || w.Session != string(se.ID) || w.Project != "pricing" || len(w.Projects) != 2 ||
		w.File != "round_test.go" || w.Line != 5 {
		t.Fatalf("where: %s", out)
	}

	// "Is this it?", with the other candidate as a row: picking it opens that file.
	if _, err := settleCall(t, s, asstCall(ua, "reply", map[string]any{"text": "Is this it?", "expects_reply": true, "items": []map[string]any{
		{"title": "src/usage.test.ts", "text": "web · 20m ago", "file": "src/usage.test.ts", "project": "web", "line": 1}}})); err != nil {
		t.Fatal(err)
	}
	s.Until("the question", func() bool { return strings.Contains(s.Text(), "Is this it?") })
	s.Press("<D-1>")
	s.Until("the other test open", func() bool {
		s.Frame()
		return s.Host().Here().File == filepath.Join(web, "src/usage.test.ts")
	})
}

// "Switch to the web project": open_project makes it the open project of the session in front.
func TestAssistantOpensAProject(t *testing.T) {
	s, ua, se := asstWorkScene(t)
	if _, err := settleCall(t, s, asstCall(ua, "open_project", map[string]any{"project": "web"})); err != nil {
		t.Fatal(err)
	}
	s.UntilState("web open", func(st core.State) bool {
		got, _ := st.Session(se.ID)
		return got.Snapshot().OpenProject() == "web"
	})
	if _, err := settleCall(t, s, asstCall(ua, "open_project", map[string]any{"project": "nope"})); err == nil ||
		!strings.Contains(err.Error(), "pricing, web") {
		t.Errorf("an unknown project: %v", err)
	}
}

func accessFind(n *render.AccessNode, label string) *render.AccessNode {
	if n == nil || n.Name == label {
		return n
	}
	for _, k := range n.Children {
		if f := accessFind(k, label); f != nil {
			return f
		}
	}
	return nil
}

// widenAssistant drags the assistant's window up by its title bar, then out by its corner, as tall as the window and
// wide enough for its side.
func widenAssistant(t *testing.T, s *apptest.Scene) {
	t.Helper()
	r := assistantOf(s).WindowRect()
	s.Drag(render.Point{X: r.Min.X + 100, Y: r.Min.Y + 16}, render.Point{X: r.Min.X + 100, Y: kit.TitleH + 16})
	r = assistantOf(s).WindowRect()
	if r.Min.Y != kit.TitleH {
		t.Fatalf("dragged by its title, the window is at %v", r)
	}
	s.Drag(render.Point{X: r.Max.X - 3, Y: r.Max.Y - 3}, render.Point{X: r.Max.X + 520, Y: r.Max.Y + 400})
	if w := assistantOf(s).WindowRect().Dx(); w < 900 {
		t.Fatalf("dragged by its corner, the window is %v wide", w)
	}
}

// The assistant runs on its provider's default model unless assistant.model says otherwise; small picks the
// provider's small one.
func TestAssistantRunsOnTheDefaultModel(t *testing.T) {
	for _, tt := range []struct{ setting, want string }{{"", "big"}, {"small", "tiny"}} {
		t.Run(cmp.Or(tt.setting, "unset"), func(t *testing.T) {
			s, lw, _ := assistantScene(t, host.Options{}, nil, nil)
			lw.Fake.Offers = []agent.Model{{ID: "big", Name: "Big", Default: true}, {ID: "tiny", Name: "Tiny", Small: true}}
			if tt.setting != "" {
				if err := s.Registry().Options().Set("assistant.model", registry.LayerUser, registry.At{}, tt.setting); err != nil {
					t.Fatal(err)
				}
			}
			s.Run("assistant.open", registry.Args{"text": "hello"})
			if ua := lw.Fake.WaitStarted(t); ua.Spec.Model != tt.want {
				t.Errorf("the assistant started on %q, want %q", ua.Spec.Model, tt.want)
			}
		})
	}
}
