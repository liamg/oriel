package agentui_test

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render/headless"
)

// The console shows each kind of block: prose, a prompt, thinking (folded), tool calls with their summaries,
// an edit as a mini-diff, shell output folded and an error; ⏎ and a click unfold.
func TestAgentConsoleShowsEachBlock(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	diff := &core.FileDiff{Path: "internal/api/usage/handler.go", Added: 1, Removed: 1, Lines: []core.DiffLine{
		{Op: " ", Old: 87, New: 87, Text: "if !res.OK() {"},
		{Op: "-", Old: 88, Text: `http.Error(w, "rate limited", 429)`},
		{Op: "+", New: 88, Text: "secs := int(time.Until(res.ResetAt()).Seconds())"},
	}}
	for _, ev := range []agent.Event{
		agent.TurnStart{},
		agent.Message{ID: "m0", Role: core.ItemUser, Text: "Add Retry-After to the 429 path"},
		agent.Message{ID: "m1", Role: core.ItemAssistant, Text: "Reading the limiter to see what it exposes."},
		agent.Message{ID: "th", Role: core.ItemThinking, Text: "The limiter has a ResetAt already."},
		agent.ToolCall{ID: "t1", Tool: "Read", Input: rig.ToolInput(map[string]string{"file_path": "internal/ratelimit/limiter.go"})},
		agent.ToolResult{ID: "t1", Output: "package ratelimit\n\nfunc New() {}"},
		agent.ToolCall{ID: "t2", Tool: "Edit", Input: rig.ToolInput(map[string]string{"file_path": "internal/api/usage/handler.go"})},
		agent.ToolResult{ID: "t2", Diff: diff},
		agent.ToolCall{ID: "t3", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "go test ./..."})},
		agent.ToolResult{ID: "t3", Output: "ok  \tpricing\t0.2s"},
		agent.Failure{Err: "the API is overloaded"},
	} {
		a.Emit(ev)
	}
	r.WaitFrame("every block", func(s string) bool {
		// Only what's in view is drawn: the read, the prose and the prompt at the top are checked once the keys reveal them.
		return apptest.HasAll(s, "Edit", "+1", "-1", "view in diff ↗", "secs := int(time.Until(res.ResetAt()).Seconds())", "Bash", "go test ./...",
			"the API is overloaded", "WORKING")
	})
	if strings.Contains(r.WaitText("Bash"), "0.2s") {
		t.Error("shell output shows before it's unfolded")
	}
	r.ClickText("Bash")
	r.WaitText("ok      pricing    0.2s")
	r.TypeKeys("kk") // from the shell call clicked: the edit, then the read
	r.WaitFrame("the read", func(s string) bool { return apptest.HasAll(s, "Read", "internal/ratelimit/limiter.go", "3 lines") })
	r.TypeKeys("k<CR>") // the thinking
	r.WaitText("The limiter has a ResetAt already.")
	r.TypeKeys("kk") // the prose, then the prompt, scrolled into view
	r.WaitFrame("the prompt", func(s string) bool {
		return apptest.HasAll(s, "YOU", "Add Retry-After to the 429 path", "Reading the limiter to see what it exposes.")
	})
}

// The side column lists the agent's subagents, newest first; one clicked shows its prompt, where it stands and what
// it said, and Escape closes it.
func TestSubagentsInTheSideColumn(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	for _, ev := range []agent.Event{
		agent.TurnStart{},
		agent.Subagent{ID: "s1", Description: "Find every 429 path", Type: "Explore", Prompt: "List each place the API answers **429**."},
		agent.Subagent{ID: "s2", Description: "Review the diff", Type: "general-purpose", Prompt: "Review it."},
		agent.Subagent{ID: "s1", Done: true, Result: "Three: the limiter, the proxy and the quota check."},
	} {
		a.Emit(ev)
	}
	r.WaitFrame("the side's subagents", func(s string) bool {
		return apptest.HasAll(s, "SUBAGENTS", "1 running", "Explore", "Find every 429 path", "general-purpose", "Review the diff")
	})
	r.ClickText("Find every 429 path")
	r.WaitFrame("its detail", func(s string) bool {
		return apptest.HasAll(s, "PROMPT", "List each place the API answers", "RESULT", "Three: the limiter, the proxy and the quota check.", "DONE")
	})
	r.TypeKeys("<Esc>")
	r.WaitGone("PROMPT")
}

// A permission's card answers the agent: Allow once on a click, Always on a, and ⇧n's note goes back with the
// denial; each card goes once it's answered.
func TestPermissionCardsAnswerTheAgent(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	allow := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "go test ./internal/api/... -run TestUsage"})})
	r.WaitFrame("the permission card", func(s string) bool {
		return apptest.HasAll(s, "Fake wants to run a command", "$ go test ./internal/api/... -run TestUsage", "rule: Bash(go test:*)", "WANTS PERMISSION")
	})
	r.ClickText("Allow once")
	if d := waitDecision(t, allow); !d.Allow || d.Always {
		t.Errorf("Allow once answered %+v", d)
	}
	r.WaitGone("Fake wants to run a command")

	always := a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "go vet ./..."})})
	r.WaitText("$ go vet ./...")
	r.TypeKeys("a")
	if d := waitDecision(t, always); !d.Allow || !d.Always {
		t.Errorf("a answered %+v", d)
	}
	r.WaitText("BASH(GO VET:*)") // the rule it made, in force

	deny := a.Ask(agent.Permission{ID: "p3", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "rm -rf build"})})
	r.WaitText("$ rm -rf build")
	r.TypeKeys("N")
	r.WaitText("Deny with this note")
	r.TypeText("use make clean")
	r.TypeKeys("<CR>")
	if d := waitDecision(t, deny); d.Allow || !strings.Contains(d.Note, "use make clean") {
		t.Errorf("the note's denial answered %+v", d)
	}
	r.WaitGone("$ rm -rf build")
}

// With the console hidden, an ask's card stacks over whatever shows, with its session's name, and answers from
// there; its session's tab counts it.
func TestAskCardsInterruptAnyView(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	r.TypeKeys("<D-A>")
	r.WaitGone("SESSION STATS")
	deny := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "curl example.com"})})
	r.WaitFrame("the interrupt card", func(s string) bool { return apptest.HasAll(s, "W · PERMISSION", "$ curl example.com") })
	r.ClickText("Deny")
	if d := waitDecision(t, deny); d.Allow {
		t.Errorf("Deny answered %+v", d)
	}
	r.WaitGone("$ curl example.com")

	// Console takes you to the session's console, where the card waits with the context around it.
	a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "curl example.org"})})
	r.WaitFrame("the second card", func(s string) bool { return apptest.HasAll(s, "W · PERMISSION", "$ curl example.org") })
	r.ClickText("Console")
	r.WaitFrame("the console, with the card in it", func(s string) bool {
		return apptest.HasAll(s, "SESSION STATS", "$ curl example.org") && !strings.Contains(s, "W · PERMISSION")
	})
}

// A question's options are buttons; a click on one answers it.
func TestQuestionCardAnswers(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	ch := a.Expect("q1")
	q := "Is RoundMinor safe to call for JPY amounts?"
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: q, Options: []string{"Safe: precision comes from the currency table", "Not yet"}}}})
	r.WaitFrame("the question", func(s string) bool { return apptest.HasAll(s, "Fake asks", q, "NEEDS YOU") })
	r.ClickText("Not yet")
	if d := waitDecision(t, ch); d.Answers[q] != "Not yet" {
		t.Errorf("answered %+v", d)
	}
	r.WaitGone(q)
}

// Interrupt in the console's header interrupts the agent's turn.
func TestConsoleInterrupts(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	r.WaitText("WORKING")
	r.ClickText("Interrupt")
	deadline := time.Now().Add(5 * time.Second)
	for a.Interrupts() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the agent was never interrupted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.WaitText("IDLE")
}

// While a turn runs the transcript ends in the working line, moving dots and how long it's run; it goes when the turn
// ends.
func TestConsoleShowsTheAgentWorking(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	fresh := regexp.MustCompile(`· (0s|<1ms|[0-9.]+ms|[0-9]\.[0-9]s)\b`)
	r.WaitFrame("the working line", func(s string) bool { return strings.Contains(s, "Fake is working") && fresh.MatchString(s) })
	r.WaitAccess("the dots read as a status", func(r *apptest.Window) bool {
		n := r.Access("Fake is working")
		return n != nil && n.Role == "status"
	})
	a.Emit(agent.TurnEnd{})
	r.WaitText("IDLE")
	r.WaitGone("Fake is working")
}

// A message written while the agent works waits as QUEUED, and goes to the agent when its turn ends.
func TestComposerQueuesWhileWorking(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	r.WaitText("WORKING")
	r.TypeKeys("i")
	r.TypeText("also check routes.go")
	r.TypeKeys("<CR>")
	r.WaitText("QUEUED")
	r.WaitText("also check routes.go")
	a.NoneSent(t)
	a.Emit(agent.TurnEnd{})
	if in := a.WaitSent(t); in.Text != "also check routes.go" {
		t.Errorf("sent %q", in.Text)
	}
	r.WaitGone("QUEUED")
}

func waitDecision(t *testing.T, ch chan agent.Decision) agent.Decision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("the agent never got an answer")
	}
	return agent.Decision{}
}

// A lost login turns its sessions needs-you with the card, the notice and the title bar's indicator
// show it, and the launcher says signed out; the card's Log in opens the agent's terminal with /login typed, and a
// login there clears every flag and the session goes on.
func TestALostLoginLogsInFromTheAgentsTerminal(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Account = "u1"
	r, a, _ := apptest.AgentWindowOn(t, lw)
	a.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "fake", Account: "u1", Plan: "Fake Pro",
		Windows: []core.UsageWindow{{Name: "Current session", Used: .3}}}})
	r.WaitText("30%")
	a.Emit(agent.TurnStart{})
	a.LoseLogin("OAuth token revoked · Please run /login")
	a.Emit(agent.TurnEnd{})
	r.WaitFrame("the card, the notice and the indicator", func(s string) bool {
		return apptest.HasAll(s, "Fake needs you to log in", "OAuth token revoked · Please run /login", "Log in", "Check again",
			"signed out", "needs you")
	})
	// The notice comes through Notify, not with the frame that shows the card.
	st := r.UntilState("the login notice", func(st core.State) bool {
		n := st.Notifications
		return len(n) > 0 && n[len(n)-1].ID == "agent.login"
	})
	if n := st.Notifications[len(st.Notifications)-1]; n.Session != "" || !strings.Contains(n.Text, "/login") {
		t.Errorf("notice = %+v", n)
	}
	r.Do(func() error { return r.Host.Run(context.Background(), "session.new", nil) })
	r.WaitText("Signed out: OAuth token revoked · Please run /login")
	r.TypeKeys("<Esc>")
	r.WaitGone("Signed out: OAuth token revoked")

	r.ClickText("Log in")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(a.Term().Shown(), "/login\r") {
		if time.Now().After(deadline) {
			t.Fatalf("the agent's terminal shows %q", a.Term().Shown())
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.WaitGone("needs you to log in")
	r.WaitGone("signed out")
	if got := a.WaitSent(t); !strings.Contains(got.Text, "signed in again") {
		t.Errorf("the agent was told %q", got.Text)
	}
}

// A turn that ends with work left running lists it at the transcript's end, and the chip says it's still working.
func TestAgentConsoleListsBackgroundWork(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{Background: []core.BackgroundTask{{Kind: "shell", Description: "Run the whole suite"}, {Kind: "subagent", Description: "Review the diff"}}})
	r.WaitFrame("the background work", func(s string) bool {
		return apptest.HasAll(s, "WORKING IN THE BACKGROUND", "IN THE BACKGROUND", "2 running · wakes the agent when done",
			"shell", "Run the whole suite", "subagent", "Review the diff")
	})
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.WaitFrame("the work done", func(s string) bool { return !strings.Contains(s, "IN THE BACKGROUND") && strings.Contains(s, "IDLE") })
}

// The composer's model menu switches the agent while it's idle, its conversation kept; mid-turn the menus are off.
func TestComposerSwitchesTheModel(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Offers = []agent.Model{{ID: "m1", Efforts: []string{"low", "high"}, Default: true}, {ID: "m2", Efforts: []string{"low", "high"}}}
	r, a, _ := apptest.AgentWindowOn(t, lw)
	r.TypeKeys("<D-A>")
	r.WaitText("SESSION STATS")
	a.Emit(agent.TurnStart{})
	r.WaitText("switch when the agent is idle")
	a.Emit(agent.TurnEnd{})
	r.WaitGone("switch when the agent is idle")
	r.ClickText("m1 ▾")
	r.ClickTop("m2")
	b := lw.Fake.WaitStarted(t)
	if b.Spec.Model != "m2" {
		t.Errorf("the agent came back on %q", b.Spec.Model)
	}
	if got := lw.Fake.Resumed(); len(got) != 1 || got[0] != a.ID() {
		t.Errorf("resumed %v, want %s: the conversation wasn't kept", got, a.ID())
	}
	r.WaitText("m2 ▾")
}

// A sandboxed session's composer says where it runs, not the gate's policy, which a sandboxed agent doesn't use.
func TestComposerShowsTheSandbox(t *testing.T) {
	r, _, _ := apptest.ConsoleWindow(t)
	r.WaitText("GATE ASK")
	st, _ := r.Store.State().Session("w")
	se := st.Snapshot()
	se.Agent.Sandboxed, se.Agent.Network = true, "ask"
	if err := r.Store.Apply(core.SessionSynced{Session: se}); err != nil {
		t.Fatal(err)
	}
	if text := r.WaitText("CONTAINER · NET ASK"); strings.Contains(text, "GATE ASK") {
		t.Errorf("a sandboxed session's composer shows the gate:\n%s", text)
	}
}

// The transcript follows the agent, but the wheel scrolls it back, and it stays there as more arrives.
func TestConsoleScrollsBack(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	for i := range 40 {
		a.Emit(agent.Message{ID: fmt.Sprint("m", i), Role: core.ItemAssistant, Text: fmt.Sprintf("Message number %02d.", i)})
	}
	r.WaitText("Message number 39.")
	r.ScrollAt("Message number 39.", -20000)
	r.Latest("the start scrolled into view", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Message number 00.") })
	a.Emit(agent.Message{ID: "m40", Role: core.ItemAssistant, Text: "Message number 40."})
	r.WaitFor("the message in the transcript", func() bool {
		se, _ := r.Store.Session("w")
		return slices.ContainsFunc(se.Snapshot().Transcript, func(it core.Item) bool { return it.ID == "m40" })
	})
	r.Do(func() error { return nil }) // the loop has taken it, and draws it next
	r.Latest("the start still in view", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Message number 00.") })
}

// A tool call's ⤢ and an ask's Details open it in full: the whole command, its input, a spinner until its result
// lands, then the result; an ask's dialog answers it as its card does.
func TestDetailsShowCallsAndAsksInFull(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	long := "go test ./internal/api/... -run 'TestUsage|TestLimits|TestRetryAfter|TestBurst|TestWindowReset|TestQuota' -count=1 -v"
	a.Emit(agent.TurnStart{})
	a.Emit(agent.ToolCall{ID: "t1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": long})})
	r.WaitText("Bash")
	r.ClickAccess("Show in full")
	r.WaitText("TOOL CALL · BASH")
	r.WaitText("waiting for the result…")
	r.WaitFrame("the whole command", func(s string) bool {
		return strings.Contains(strings.ReplaceAll(s, "\n", ""), "TestQuota' -count=1 -v")
	})
	a.Emit(agent.ToolResult{ID: "t1", Output: "ok  \tapi\t0.4s\n"})
	r.WaitText("RESULT")
	r.WaitGone("waiting for the result…")
	r.TypeKeys("<Esc>")
	r.WaitGone("TOOL CALL · BASH")

	allow := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": long})})
	r.WaitText("Fake wants to run a command")
	r.ClickText("Details")
	r.WaitText("ASK · PERMISSION")
	r.WaitText("INPUT")
	r.ClickTop("Allow once")
	if d := waitDecision(t, allow); !d.Allow {
		t.Errorf("the dialog's Allow once answered %+v", d)
	}
	r.WaitGone("ASK · PERMISSION")
}

// A message queued behind the agent's turn has a ✕ that takes it back before the agent gets it; the rest still go.
func TestQueuedConsoleMessageCancels(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	r.WaitText("WORKING")
	r.TypeKeys("i")
	for _, m := range []string{"also check routes.go", "and the middleware"} {
		r.TypeText(m)
		r.TypeKeys("<CR>")
		r.WaitText(m)
	}
	r.WaitFrame("both queued", func(s string) bool { return strings.Count(s, "QUEUED") == 2 })
	r.ClickAccess("Cancel queued: also check routes.go")
	r.WaitGone("also check routes.go")
	a.Emit(agent.TurnEnd{})
	if in := a.WaitSent(t); in.Text != "and the middleware" {
		t.Errorf("sent %q", in.Text)
	}
	a.NoneSent(t)
}

// A queued message's edit takes it out of the queue into the composer, focused, to send again once changed.
func TestQueuedConsoleMessageEdits(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.TurnStart{})
	r.WaitText("WORKING")
	r.TypeKeys("i")
	r.TypeText("also check routes.go")
	r.TypeKeys("<CR>")
	r.WaitText("QUEUED")
	r.ClickText("edit")
	r.WaitGone("QUEUED")
	r.TypeText(" and the tests")
	r.TypeKeys("<CR>")
	r.WaitText("QUEUED")
	a.Emit(agent.TurnEnd{})
	if in := a.WaitSent(t); in.Text != "also check routes.go and the tests" {
		t.Errorf("sent %q", in.Text)
	}
}

// A permission that comes while you type in a text field waits for you to stop before it goes over the window, at
// the top: it doesn't cover what you're writing or catch a key meant for it.
func TestAskWaitsForTypingToStop(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	r.TypeKeys("<D-A>")
	r.WaitGone("SESSION STATS")
	r.TypeKeys("<D-R>")
	r.ClickText("New request")
	r.WaitText("Write a request")
	r.ClickText("Write a request")
	r.TypeText("round")
	r.WaitText("round")
	a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "curl example.com"})})
	for _, ch := range " half even" {
		r.TypeText(string(ch))
		time.Sleep(80 * time.Millisecond)
		if fs := r.Win.Frames(); len(fs) > 0 && strings.Contains(fs[len(fs)-1].Text(), "$ curl example.com") {
			t.Fatal("the card went over the window while typing")
		}
	}
	r.WaitFrame("the card, once typing stopped", func(s string) bool { return apptest.HasAll(s, "W · PERMISSION", "$ curl example.com") })
	r.WaitText("round half even") // nothing typed went anywhere else
}

// Text in the console selects and copies with no opting in: a triple click on the agent's message, then the copy key,
// copies it; the same for a tool call's output.
func TestConsoleTextCopies(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	for _, ev := range []agent.Event{
		agent.TurnStart{},
		agent.Message{ID: "m1", Role: core.ItemAssistant, Text: "Reading the limiter to see what it exposes."},
		agent.ToolCall{ID: "t3", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "go test ./..."})},
		agent.ToolResult{ID: "t3", Output: "ok  \tpricing\t0.2s"},
	} {
		a.Emit(ev)
	}
	copyKey := "<C-S-c>"
	if runtime.GOOS == "darwin" {
		copyKey = "<D-c>"
	}
	tripleCopy := func(text string) {
		t.Helper()
		r.ClickTextTimes(text, 3)
		r.TypeKeys(copyKey)
	}
	r.WaitText("go test ./...") // all of it in, so nothing arriving moves the message
	r.WaitText("Reading the limiter to see what it exposes.")
	tripleCopy("Reading the limiter to see what it exposes.")
	waitClip(t, r, "Reading the limiter to see what it exposes.")

	r.ClickText("Bash")
	out := "ok      pricing    0.2s"
	r.WaitText(out)
	tripleCopy(out)
	waitClip(t, r, out)
}

// A dialog only the agent's terminal answers makes its session needs-you with a card saying what it asks; the
// card's Go to terminal opens that terminal, and the card goes once the dialog is answered.
func TestATerminalDialogGoesToTheTerminal(t *testing.T) {
	r, a, _ := apptest.ConsoleWindow(t)
	a.Emit(agent.Dialog{Title: "Allow external CLAUDE.md file imports?", Shown: true})
	r.WaitFrame("the card", func(s string) bool {
		return apptest.HasAll(s, "Fake waits on its terminal", "Allow external CLAUDE.md file imports?", "Go to terminal", "needs you")
	})
	r.ClickText("Go to terminal")
	r.WaitText("agent · " + string(r.Host.State().Focus))
	a.Emit(agent.Dialog{})
	r.WaitGone("Fake waits on its terminal")
}
