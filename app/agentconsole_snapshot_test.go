package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// consoleScene is mockup 15's session in front, with its console open: a Codex turn with reads, a search, edits,
// subagents, a permission and a network ask, a queued message, and both providers' plan usage.
func consoleScene(t *testing.T) *errScene {
	t.Helper()
	s := newScene(t, "", "", mockupSize)
	s.now = at(14, 30, 0)
	ago := func(sec int) time.Time { return s.now.Add(-time.Duration(sec) * time.Second) }
	var read []string
	for i := range 142 {
		read = append(read, fmt.Sprintf("line %d", i))
	}
	se := core.SessionState{ID: "usage-api", Name: "INF-2231 usage api", Kind: core.KindWork, Status: core.StatusPermission, Ticket: "INF-2231",
		Projects: []core.Project{{Name: "usage-api", Dir: "/src/usage-api", Branch: "rethink/usage-endpoint", Primary: true}},
		Agent: core.AgentState{Provider: "codex", Model: "gpt-5.5-codex", Effort: "high", Running: true, Working: true, TurnStarted: s.now,
			Policy: "light", Sandboxed: true, Network: "ask",
			Queue: []string{"Also check @internal/api/usage/routes.go: the batch endpoint has its own limiter."},
			Tally: core.Tally{Context: 118_000, ContextMax: 400_000, Input: 184_600, CacheRead: 1_235_400, Output: 38_000, Turns: 14, ToolCalls: 63,
				Cost: 3.10, Running: 4320}},
		Threads: []core.Thread{{ID: "t1", Status: core.ThreadOpen, Busy: true, Anchor: &core.Anchor{File: "internal/api/usage/handler.go", Line: 88},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorYou, Body: "The 429 path never sets `Retry-After`. Add it from the limiter's reset time and cover it in the handler test."}}}},
		Transcript: []core.Item{
			{ID: "u", Kind: core.ItemUser, Time: ago(80), Text: "Requests to handle:\n\n1. internal/api/usage/handler.go:88\nThe 429 path never sets Retry-After.\nAdd it from the limiter's reset time."},
			{ID: "a1", Kind: core.ItemAssistant, Time: ago(70), Text: "Reading the limiter to see what it exposes for the reset time."},
			{ID: "r1", Kind: core.ItemTool, Tool: "Read", Title: "Read internal/ratelimit/limiter.go", Done: true, Output: strings.Join(read, "\n"),
				Duration: 100, Time: ago(66)},
			{ID: "g1", Kind: core.ItemTool, Tool: "Grep", Title: `Search "Reserve(" in internal/`, Done: true, Output: "a.go:1\nb.go:2\nc.go:3\nd.go:4",
				Duration: 200, Time: ago(60)},
			{ID: "th", Kind: core.ItemThinking, Time: ago(55), Text: "The reservation knows when it resets.\nUse that."},
			{ID: "a2", Kind: core.ItemAssistant, Time: ago(50), Text: "`Reservation.ResetAt()` already exists. Writing the header from it:"},
			{ID: "e1", Kind: core.ItemTool, Tool: "Edit", Title: "Edit internal/api/usage/handler.go", Done: true, Time: ago(45),
				Diff: &core.FileDiff{Path: "internal/api/usage/handler.go", Added: 3, Removed: 1, Lines: []core.DiffLine{
					{Op: " ", Old: 87, New: 87, Text: "\tif !res.OK() {"},
					{Op: "-", Old: 88, Text: "\t\thttp.Error(w, \"rate limited\", http.StatusTooManyRequests)"},
					{Op: "+", New: 88, Text: "\t\tsecs := int(math.Ceil(time.Until(res.ResetAt()).Seconds()))"},
					{Op: "+", New: 89, Text: "\t\tw.Header().Set(\"Retry-After\", strconv.Itoa(max(secs, 1)))"},
					{Op: "+", New: 90, Text: "\t\thttp.Error(w, \"rate limited\", http.StatusTooManyRequests)"},
					{Op: " ", Old: 89, New: 91, Text: "\t\treturn"},
				}}},
			{ID: "e2", Kind: core.ItemTool, Tool: "Edit", Title: "Edit internal/api/usage/handler_test.go", Done: true, Time: ago(40),
				Diff: &core.FileDiff{Path: "internal/api/usage/handler_test.go", Added: 18}},
			{ID: "b1", Kind: core.ItemTool, Tool: "Bash", Title: "Bash go vet ./internal/api/...", Done: true, Output: "ok", Duration: 2300, Time: ago(30)},
		},
		Subagents: []core.Subagent{
			{ID: "s1", Type: "explorer", Description: "Find every 429 path in the API and whether it sets Retry-After", Running: true, Started: ago(38), Tools: 11},
			{ID: "s2", Type: "docs", Description: "Check the public API docs mention Retry-After", Running: true, Started: ago(12), Tools: 3},
		},
		Asks: []core.Ask{
			{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "go test ./internal/api/... -run TestUsage -count=1", Rule: "Bash(go test:*)",
				Created: ago(5)},
			{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "go mod download · first request from this workspace · egress proxy",
				Created: ago(2)},
		},
		Rules: []string{"Bash(go vet:*)"},
	}
	for _, ev := range []core.Event{
		core.SessionSynced{Session: se},
		core.SessionFocused{ID: se.ID},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "codex", Plan: "ChatGPT Pro", Windows: []core.UsageWindow{
			{Name: "5h window", Used: .22, Resets: s.now.Add(3*time.Hour + 10*time.Minute)}, {Name: "weekly", Used: .48, Resets: s.now.Add(50 * time.Hour)}}}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "Claude Max", Windows: []core.UsageWindow{
			{Name: "5h window", Used: .38}, {Name: "weekly", Used: .71}}}},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// AI-6, AI-7: the console (15-agent) with its blocks, a permission and a network card where they arose, the
// subagents, the composer's queue and the tally.
func TestAgentConsoleSnapshot(t *testing.T) {
	s := consoleScene(t)
	s.run("view.open", registry.Args{"id": AgentConsoleView, "place": "tab", "title": "Agent"})
	s.frame()
	s.lint("the agent console")
	s.golden("agent-console")
}

// AI-7: with the console hidden, every session's asks stack at the bottom right over the window, each with its
// session's name: a permission, a network connection, a question and a gateway call, then a count of the rest; the
// tabs count them.
func TestAskCardsSnapshot(t *testing.T) {
	s := consoleScene(t)
	other := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindWork, Status: core.StatusNeedsYou,
		Agent: core.AgentState{Provider: "claude", Running: true},
		Asks: []core.Ask{
			{ID: "q1", Kind: core.AskQuestion, Questions: []core.Question{{Question: "Is pricing.RoundMinor safe to call for JPY amounts, or does it assume 2 decimal places?",
				Options: []string{"Safe: precision comes from the currency table", "Not yet: lands with rethink/rounding"}}}},
			{ID: "g1", Kind: core.AskGateway, Tool: "git push", Summary: "git push origin rethink/rounding", Detail: "3 commits · +184 -22 · 0 unreviewed lines ✓",
				Rule: "git push origin rethink/*"},
			{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "requested by go mod download · CONNECT · no TLS interception"},
			{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "make e2e"},
		}}
	if err := s.store.Apply(core.SessionSynced{Session: other}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	s.lint("the ask cards")
	s.golden("ask-cards")
}
