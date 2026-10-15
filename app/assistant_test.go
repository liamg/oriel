package app

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/voice/voicetest"
	"github.com/liamg/oriel/worker"
)

var asstNow = time.Date(2026, 10, 15, 14, 5, 0, 0, time.UTC)

func (s *errScene) assistant() *assistantExt {
	for _, e := range s.exts {
		if x, ok := e.(*assistantExt); ok {
			return x
		}
	}
	return nil
}

// assistantScene is the app on a local worker with the fake provider, at the mockup's size and a fixed clock, with
// a scripted microphone and a speaker that keeps what it's given.
func assistantScene(t *testing.T, o worker.Options, mic *voicetest.Mic, tr *voicetest.Transcriber) (*errScene, *localWorker, *saidSpeaker) {
	t.Helper()
	lw := newWorkerWith(t, o)
	sp := &saidSpeaker{}
	if mic == nil {
		mic, tr = &voicetest.Mic{}, &voicetest.Transcriber{}
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		lw.connect(t)(cfg)
		cfg.Voice = VoiceConfig{Mic: mic, Transcriber: tr, Speaker: sp}
	})
	s.now = asstNow
	s.assistant().conv.Now = func() time.Time { return s.now }
	waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	s.frame()
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
func (s *errScene) settleCall(t *testing.T, done chan [2]any) (string, error) {
	t.Helper()
	var r [2]any
	s.until(t, "the tool's answer", func() bool {
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
func (s *errScene) started(t *testing.T, lw *localWorker) *agenttest.FakeSession {
	t.Helper()
	var a *agenttest.FakeSession
	s.until(t, "an agent to start", func() bool {
		select {
		case a = <-lw.fake.Started:
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

// ui.md 11: "The room's assistant as a drawer: 'what needs me?' … Answers come back as actionable rows (number keys
// pick one) … It shows what it's allowed to do, and its data sources with how fresh each is." agents.md: the
// assistant is a resident utility session, never claude -p.
func TestAssistantWhatNeedsMe(t *testing.T) {
	s, lw, _ := assistantScene(t, worker.Options{}, nil, nil)
	s.run("session.start", registry.Args{"kind": "work", "name": "rounding", "provider": "fake", "repos": []string{lw.repo}})
	work := lw.fake.WaitStarted(t)
	work.Ask(agent.Permission{ID: "p1", Tool: "Bash", Summary: "make e2e"})
	s.run("session.start", registry.Args{"kind": "work", "name": "other", "provider": "fake", "repos": []string{lw.repo}})
	lw.fake.WaitStarted(t)
	waitState(t, s.store, "rounding waiting on a permission", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && se.Status() == core.StatusPermission && st.Focus == "other"
	})

	// The window follows the session in front, out of the start view, which keeps to its own keys.
	s.until(t, "the window to follow the session", s.reopen)
	s.assistant().conv.Now = func() time.Time { return s.now }
	s.frame()

	// ⌘/ pulls it down over a page that's open, with the composer focused; esc puts it away, leaving the page.
	s.run("settings.open", nil)
	s.frame()
	s.keyCommand(t, "<D-/>")
	s.frame()
	if !s.assistant().open {
		t.Fatal("⌘/ didn't open the assistant")
	}
	s.wantFrame(t, "COMMS", "ASSISTANT", "back to editor", "WHAT IT CAN DO", "SOURCES", "RECENT ACTIONS")
	s.keyCommand(t, "<Esc>")
	if s.assistant().open || !s.settings().open {
		t.Fatalf("esc: assistant open %v, settings open %v", s.assistant().open, s.settings().open)
	}
	s.settings().open = false
	s.keyCommand(t, "<D-/>")
	s.frame()

	s.typeInto(t, "Ask the assistant", "what needs me?")
	s.keyCommand(t, "<CR>")
	ua := lw.fake.WaitStarted(t)
	if ua.Spec.Pack.Skill != "assistant" || !strings.Contains(ua.Spec.Pack.Text, "Oriel's assistant") || ua.Spec.Policy != agent.PolicyAsk {
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
	out, err := s.settleCall(t, asstCall(ua, "sessions", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var lines []sessionLine
	if json.Unmarshal([]byte(out), &lines) != nil || len(lines) != 2 || lines[0].ID != "rounding" || lines[0].Status != "permission" ||
		len(lines[0].Asks) != 1 || lines[0].Asks[0] != "Bash make e2e" || lines[0].Kind != "work" {
		t.Fatalf("sessions: %s", out)
	}
	if _, err := s.settleCall(t, asstCall(ua, "reply", map[string]any{"text": "One thing waits on you.", "items": []map[string]any{
		{"title": "rounding", "text": "wants to run make e2e", "meta": "1m · this machine", "session": "rounding"}}})); err != nil {
		t.Fatal(err)
	}
	ua.Emit(agent.TurnEnd{})
	s.until(t, "the answer", func() bool { return strings.Contains(s.frameText(), "One thing waits on you.") })
	s.wantFrame(t, "what needs me?", "✓ SESSIONS", "rounding", "wants to run make e2e", "1m · this machine", "⌘1",
		"start session", "asks first", "never", "sessions", "github", "web search", "now")
	if o := s.ui.Overflowing(); len(o) > 0 {
		t.Errorf("text drawn past its box:\n%s", strings.Join(o, "\n"))
	}
	s.lint("the assistant's drawer")

	// ⌘1 picks the first row: its session comes to the front, and the drawer goes.
	s.keyCommand(t, "<D-1>")
	waitState(t, s.store, "rounding in front", func(st core.State) bool { return st.Focus == "rounding" })
	if s.assistant().open {
		t.Error("the drawer stayed over the session it opened")
	}
}

// crit-reference.md, "Assistant": "Confirmation cards". ui.md 11: "Every action has an undo with a countdown." An
// act shows what it will do and waits; y does it, n doesn't, and what it made can be undone for a few seconds.
func TestAssistantAsksFirst(t *testing.T) {
	s, lw, _ := assistantScene(t, worker.Options{}, nil, nil)
	c, _ := s.sh.sessions.worker("")
	if _, err := c.AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s.run("assistant.open", registry.Args{"text": "plan a cache for pricing"})
	ua := lw.fake.WaitStarted(t)

	done := asstCall(ua, "start_session", map[string]any{"repo": "pricing", "prompt": "Add a cache in front of the price lookups."})
	s.until(t, "the card", func() bool { return strings.Contains(s.frameText(), "Start a plan session") })
	s.wantFrame(t, "CONFIRM · START_SESSION", "The assistant wants to start a plan session in pricing on", "repository", "pricing — "+lw.repo,
		"THE PROMPT THE AGENT WILL BE GIVEN", "Add a cache in front of the price lookups.", "A plan session drafts a plan first", "Yes", "No", "120s")
	if len(s.store.State().Sessions) != 0 {
		t.Fatal("a session started before the yes")
	}
	s.lint("a confirmation")
	s.typeKeys("y")
	out, err := s.settleCall(t, done)
	if err != nil || !strings.Contains(out, `"session"`) {
		t.Fatalf("start_session: %q %v", out, err)
	}
	st := waitState(t, s.store, "the plan session", func(st core.State) bool { return len(st.Sessions) == 1 })
	planned := st.Sessions[0]
	if planned.Kind != core.KindPlan || planned.Prompt != "Add a cache in front of the price lookups." || st.Focus == planned.ID {
		t.Errorf("started %s %q, focus %q: it starts behind the session in front", planned.Kind, planned.Prompt, st.Focus)
	}
	s.until(t, "the action", func() bool { return strings.Contains(s.frameText(), "✓ started a plan session in pricing") })
	s.wantFrame(t, "you said yes", "Open", "Undo 10s", "RECENT ACTIONS")
	s.clickText(t, "Undo 10s")
	s.until(t, "the session deleted", func() bool { return len(s.store.State().Sessions) == 0 })
	s.until(t, "undone", func() bool { return strings.Contains(s.frameText(), "undone: started a plan session") })

	// No: nothing starts, and the agent is told so.
	done = asstCall(ua, "review_pr", map[string]any{"pr": "acme/api#4410"})
	s.until(t, "the review's card", func() bool { return strings.Contains(s.frameText(), "Start a review") })
	s.wantFrame(t, "acme/api", "#4410", "https://github.com/acme/api/pull/4410")
	s.typeKeys("n")
	if _, err := s.settleCall(t, done); err == nil || !strings.Contains(err.Error(), "said no") {
		t.Errorf("a no: %v", err)
	}
	s.until(t, "the no", func() bool { return strings.Contains(s.frameText(), "you said no — nothing was done") })
	if len(s.store.State().Sessions) != 0 {
		t.Error("a review started on a no")
	}
}

// PL-11's VoiceWake hook: with the assistant's wake option on (the default), what follows the wake word goes to it,
// marked spoken, and its answer is said aloud.
func TestTheWakeWordGoesToTheAssistant(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(1200*time.Millisecond), voicetest.Speech(2*time.Second),
		voicetest.Silence(time.Second))}
	tr := &voicetest.Transcriber{Word: "x", Text: func(string) string { return "Computer, what needs me?" }}
	s, lw, sp := assistantScene(t, worker.Options{}, mic, tr)
	s.reg.Options().Set("voice.wake", registry.LayerUser, registry.At{}, true)
	ua := s.started(t, lw)
	s.reg.Options().Set("voice.wake", registry.LayerUser, registry.At{}, false)
	if !strings.HasSuffix(ua.Spec.Prompt, ", spoken] what needs me?") {
		t.Errorf("the spoken turn: %q", ua.Spec.Prompt)
	}
	s.until(t, "the drawer", func() bool { return s.assistant().open })
	if _, err := s.settleCall(t, asstCall(ua, "reply", map[string]any{"text": "Nothing needs you."})); err != nil {
		t.Fatal(err)
	}
	ua.Emit(agent.TurnEnd{})
	s.until(t, "it said so", func() bool { return slices.Equal(sp.words(), []string{"Nothing needs you."}) })

	s.reg.Options().Set("assistant.wake", registry.LayerUser, registry.At{}, false)
	if v, _ := s.reg.Fire(context.Background(), "VoiceWake", "", &VoiceWake{Text: "hello"}); v.Decided {
		t.Error("the assistant took a wake request with assistant.wake off")
	}
}

// my_tickets (the tickets'), mission control (SH-7) and agentbus's request_review (PL-5) are their features' tools:
// a feature or plugin offers them for the assistant through the registry (this my_tickets shadows the app's), and they reach it, and only it, like its own. An act
// of theirs asks first just the same.
func TestPluginToolsReachTheAssistant(t *testing.T) {
	s, lw, _ := assistantScene(t, worker.Options{}, nil, nil)
	requested := ""
	for _, tool := range []registry.AgentTool{
		{Name: "my_tickets", For: []string{assistantAgent}, ReadOnly: true, Reads: "linear", Desc: "The user's Linear tickets.",
			Run: func(registry.Ctx, map[string]any) (any, error) {
				return []map[string]string{{"key": "INF-2231", "title": "usage api", "state": "In Progress"}}, nil
			}},
		{Name: "mission_control", For: []string{assistantAgent}, ReadOnly: true, Reads: "mission control", Desc: "What mission control shows.",
			Run: func(registry.Ctx, map[string]any) (any, error) { return "2 PRs failing CI", nil }},
		{Name: "request_review", For: []string{assistantAgent}, Params: map[string]string{"pr": "string", "who": "string?"},
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
		s.reg.Contribute(tool)
	}
	s.run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": []string{lw.repo}})
	if names := toolNames(lw.fake.WaitStarted(t)); slices.Contains(names, "my_tickets") || slices.Contains(names, "request_review") {
		t.Errorf("a session's agent was offered the assistant's: %v", names)
	}
	s.run("assistant.open", registry.Args{"text": "my tickets?"})
	ua := lw.fake.WaitStarted(t)
	for _, want := range []string{"my_tickets", "mission_control", "request_review"} {
		if !slices.Contains(toolNames(ua), want) {
			t.Errorf("no %s for the assistant", want)
		}
	}
	ua.Emit(agent.ToolCall{ID: "c1", Tool: "mcp__oriel__my_tickets"})
	if out, err := s.settleCall(t, asstCall(ua, "my_tickets", map[string]any{})); err != nil || !strings.Contains(out, "INF-2231") {
		t.Errorf("my_tickets: %q %v", out, err)
	}
	s.until(t, "the source", func() bool { return strings.Contains(s.frameText(), "linear") })
	s.wantFrame(t, "mission control", "request review", "asks first", "✓ MY_TICKETS")

	// With the drawer away, its card stacks over the window with the inbox's (ui.md 12), and is answered there.
	s.run("assistant.close", nil)
	done := asstCall(ua, "request_review", map[string]any{"pr": "acme/api#4421", "who": "maria"})
	s.until(t, "its card", func() bool { return strings.Contains(s.frameText(), "Ask for a review") })
	if requested != "" || s.assistant().open {
		t.Fatal("it ran before the yes, or the drawer came down for it")
	}
	s.wantFrame(t, "ASSISTANT · REQUEST_REVIEW", "The assistant wants to ask @maria to review acme/api#4421")
	s.clickText(t, "Yes")
	if _, err := s.settleCall(t, done); err != nil || requested != "maria" {
		t.Errorf("request_review: %v, %q", err, requested)
	}
	s.run("assistant.open", nil)
	s.until(t, "what it did", func() bool { return strings.Contains(s.frameText(), "✓ asked @maria to review acme/api#4421") })
}

// crit-reference.md, "PR lists": "Mine and review-requested". The assistant reads them from GitHub through the
// worker's gh, and says which has a session on it.
func TestAssistantReadsPullRequests(t *testing.T) {
	hub := ghtest.New(t)
	hub.AddPR("acme/api", gh.PR{Title: "retry transient 502s", Author: "maria", Reviewers: []string{"you"}})
	mine := hub.AddPR("acme/api", gh.PR{Title: "round half to even", Draft: true})
	s, lw, _ := assistantScene(t, worker.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }}, nil, nil)
	s.run("assistant.say", registry.Args{"text": "my PRs?"})
	ua := lw.fake.WaitStarted(t)
	out, err := s.settleCall(t, asstCall(ua, "my_prs", map[string]any{}))
	var prs []prLine
	if err != nil || json.Unmarshal([]byte(out), &prs) != nil || len(prs) != 1 || prs[0].Title != "round half to even" || !prs[0].Draft ||
		prs[0].URL != mine.URL || prs[0].Repo != "acme/api" {
		t.Errorf("my_prs: %s %v", out, err)
	}
	out, err = s.settleCall(t, asstCall(ua, "review_requests", map[string]any{}))
	if err != nil || !strings.Contains(out, "retry transient 502s") || !strings.Contains(out, `"author": "maria"`) {
		t.Errorf("review_requests: %s %v", out, err)
	}
}

// scripting.md: a Lua agent tool says who it's for, so init.lua can give the assistant a source of its own.
func TestLuaToolsForTheAssistant(t *testing.T) {
	s := luaScene(t, `oriel.agent.tool{ name = "deploys", desc = "Today's deploys", ["for"] = { "assistant" }, read_only = true,
  reads = "deploys", run = function() return "2 today" end }`)
	tools := registry.AgentToolsFor(s.reg, assistantAgent, true)
	i := slices.IndexFunc(tools, func(t registry.AgentTool) bool { return t.Name == "deploys" })
	if i < 0 || !tools[i].ReadOnly || tools[i].Reads != "deploys" {
		t.Fatalf("the assistant's tools: %+v", tools)
	}
	if slices.ContainsFunc(registry.AgentToolsFor(s.reg, "work", false), func(t registry.AgentTool) bool { return t.Name == "deploys" }) {
		t.Error("a work session's agent was offered the assistant's tool")
	}
	out, err := s.callTool(t, registry.WithAgent(context.Background(), assistantAgent), tools[i], nil)
	if err != nil || out != "2 today" {
		t.Errorf("deploys: %q %v", out, err)
	}
}
