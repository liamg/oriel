package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
)

// requestsSession is agentSession with the Requests view open from the rail's key, ⌘⇧R.
func requestsSession(t *testing.T) (*running, *agenttest.FakeSession, *localHost) {
	t.Helper()
	r, a, lw := agentSession(t)
	r.typeKeys("<D-R>")
	r.waitText("New request")
	return r, a, lw
}

// threadWith waits for the session's thread whose first comment is body.
func threadWith(t *testing.T, r *running, body string) core.Thread {
	t.Helper()
	var th core.Thread
	waitState(t, r.store, "the thread "+body, func(st core.State) bool {
		se, ok := st.Session("w")
		if !ok {
			return false
		}
		for _, x := range se.Snapshot().Threads {
			if len(x.Comments) > 0 && x.Comments[0].Body == body {
				th = x
				return true
			}
		}
		return false
	})
	return th
}

// projectDir is the session's worktree.
func projectDir(t *testing.T, r *running) string {
	t.Helper()
	se, ok := r.store.Session("w")
	if !ok {
		t.Fatal("no session w")
	}
	p, _ := se.Snapshot().Primary()
	return p.Dir
}

// newRequest writes a request in the composer and sends it with ⏎, returning when the agent has it and how long that
// took from the key.
func newRequest(t *testing.T, r *running, a *agenttest.FakeSession, body string) (agent.Input, time.Duration) {
	t.Helper()
	r.clickText("New request")
	r.waitText("Write a request")
	r.typeText(body)
	r.waitText(body)
	start := time.Now()
	r.typeKeys("<CR>")
	in := a.WaitSent(t)
	return in, time.Since(start)
}

// AI-5, done when: "a comment reaches the agent within 1 s". The ticket's own bound; the fake agent is idle, so the
// host hands it over at once (agents.md, "Comments to agent").
func TestACommentReachesTheAgentWithinASecond(t *testing.T) {
	r, a, _ := requestsSession(t)
	in, took := newRequest(t, r, a, "Negative values round the wrong way")
	if took > time.Second {
		t.Errorf("the comment took %v to reach the agent", took)
	}
	if !strings.Contains(in.Text, "Negative values round the wrong way") {
		t.Errorf("the agent got %q", in.Text)
	}
	r.waitFrame("the request, working", func(s string) bool {
		return containsAll(s, "WORKING", "Negative values round the wrong way", "FAKE · WORKING")
	})
}

// The request the agent is on ends in its working line, moving dots read out as who's working, until it answers.
func TestABusyRequestShowsTheAgentWorking(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	a.Emit(agent.TurnStart{})
	r.waitFrame("the working line", func(s string) bool { return containsAll(s, "Negative values round the wrong way", "Fake is working") })
	if n := r.access("Fake is working"); n == nil || n.Role != "status" {
		t.Errorf("the dots read as %+v", n)
	}
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Fixed."})
	a.Emit(agent.TurnEnd{})
	r.waitText("Fixed.")
	r.waitGone("Fake is working")
}

// runIn is the first glyph run in f whose text is s.
func runIn(f headless.Frame, s string) (render.Op, bool) {
	for _, op := range f.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == s {
			return op, true
		}
	}
	return render.Op{}, false
}

// AI-5, done when: "replies render as markdown with activity blocks"; TR-5: the reply's "tests pass" links to the
// run behind it, in the request and in the console, and the link opens the run.
func TestRepliesRenderAsMarkdownWithActivity(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	dir := projectDir(t, r)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.ToolCall{ID: "e1", Tool: "Edit", Input: toolInput(map[string]string{"file_path": filepath.Join(dir, "internal/pricing/round.go")})})
	a.Emit(agent.ToolResult{ID: "e1", Diff: &core.FileDiff{Path: filepath.Join(dir, "internal/pricing/round.go"), Added: 7, Removed: 1}})
	a.Emit(agent.ToolCall{ID: "b1", Tool: "Bash", Input: toolInput(map[string]string{"command": "go test ./internal/pricing/"})})
	a.Emit(agent.ToolResult{ID: "b1", Output: "--- PASS: TestRoundHalfEven (0.00s)\nok  \tpricing\t0.01s\n"})
	waitState(t, r.store, "the run", func(st core.State) bool {
		s, _ := st.Session("w")
		return s != nil && len(s.Snapshot().Runs) == 1
	})
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Right: `Floor` rounds toward -∞.\n\n- made the negative cases table-driven\n- checked the `RoundMinor` callers\n\nThe tests pass."})
	f := r.latest("the reply as markdown, with its activity and evidence", func(f headless.Frame) bool {
		return containsAll(f.Text(), "Right:", "rounds toward -∞.", "•", "made the negative cases table-driven", "Edit", "round.go", "+7", "-1",
			"2 passed", "The tests pass.")
	})
	if op, ok := runIn(f, "Floor"); !ok || op.Run.Font.Family != typeset.Mono {
		t.Errorf("inline code isn't a mono run of its own: %+v", op.Run)
	}
	r.clickText("2 passed")
	r.waitFrame("the run it links to", func(s string) bool {
		return containsAll(s, "go test ./internal/pricing/", "--- PASS: TestRoundHalfEven (0.00s)", "BY AGENT")
	})

	r.typeKeys("<D-A>")
	r.waitText("SESSION STATS")
	r.waitFrame("the console's evidence on the test run", func(s string) bool { return containsAll(s, "go test ./internal/pricing/", "2 passed") })

	r.do(func() error { return r.host.Run(context.Background(), "tests.open", nil) })
	r.waitFrame("the agent's run in the Tests panel", func(s string) bool { return containsAll(s, "TESTS", "AGENT", "2 passed") })
}

// AI-5: urgent interrupts the turn, and btw is answered by a fork, outside the conversation.
func TestUrgentInterruptsAndBtwAsksAFork(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	threadWith(t, r, "Negative values round the wrong way")
	a.Emit(agent.TurnStart{})
	r.waitText("FAKE · WORKING")
	r.clickText("URGENT · INTERRUPTS TURN")
	r.clickText("Reply, or ask more")
	r.typeText("negative cases first")
	r.typeKeys("<CR>")
	got := a.WaitSent(t)
	if a.Interrupts() != 1 || !strings.Contains(got.Text, "negative cases first") || !strings.Contains(got.Text, `urgent="true"`) {
		t.Errorf("interrupted %d times; sent %q", a.Interrupts(), got.Text)
	}
	answered(t, r.store, a)
	a.Emit(agent.TurnEnd{})

	r.clickText("BTW")
	r.clickText("Reply, or ask more")
	r.typeText("why decimal and not big.Rat?")
	r.typeKeys("<CR>")
	r.waitText("fork says:")
	a.NoneSent(t)
}

// AI-5: a thread opens between the lines from the editor, reaches the agent anchored there, shows the agent's reply
// in place, and takes a reply of its own.
func TestInlineThreadsInTheEditor(t *testing.T) {
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.waitText("floor := scaled.Floor()")
	r.typeKeys("4G")
	r.waitFrame("the cursor on line 4", func(s string) bool { return strings.Contains(s, "\n4:") })
	r.do(func() error { return r.host.Run(context.Background(), "thread.new", nil) })
	r.waitText("New thread")
	r.typeText("Floor goes the wrong way for negatives")
	r.waitText("Floor goes the wrong way for negatives")
	r.typeKeys("<CR>")
	in := a.WaitSent(t)
	if !strings.Contains(in.Text, "round.go:4") || !strings.Contains(in.Text, "Floor goes the wrong way for negatives") {
		t.Errorf("the agent got %q", in.Text)
	}
	th := threadWith(t, r, "Floor goes the wrong way for negatives")
	r.waitFrame("the thread under its line", func(s string) bool {
		return containsAll(s, "Thread", "L4", "FAKE · WORKING", "Floor goes the wrong way for negatives")
	})
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Switching to `Truncate` with sign handling."})
	a.Emit(agent.TurnEnd{})
	r.waitText("with sign handling.")
	r.clickText("Reply…")
	r.typeText("and cover -2.5")
	r.typeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "and cover -2.5") {
		t.Errorf("the inline reply sent %q", in.Text)
	}
}

// SH-5 meets AI-5: ⌃a in a picker attaches the item to the request being written, and ␣r picks a thread.
func TestPickersAttachAndPickThreads(t *testing.T) {
	r, a, _ := requestsSession(t)
	os.WriteFile(filepath.Join(projectDir(t, r), "invoice.go"), []byte("package billing\n"), 0o644)
	r.clickText("New request")
	r.waitText("Write a request")
	r.clickText("@ FILE")
	r.typeText("invoice")
	r.waitText("invoice.go")
	r.typeKeys("<C-a>")
	r.waitText("@invoice.go")
	r.clickText("Write a request")
	r.typeText("don't touch stored invoices")
	r.typeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "invoice.go") || !strings.Contains(in.Text, "don't touch stored invoices") {
		t.Errorf("sent %q", in.Text)
	}
	threadWith(t, r, "don't touch stored invoices\n\n@invoice.go")

	r.do(func() error {
		return r.host.Run(context.Background(), "editor.open", registry.Args{"path": filepath.Join(projectDir(t, r), "invoice.go")})
	})
	r.waitText("package billing")
	r.typeKeys("<Space>r")
	r.waitFrame("the threads picker", func(s string) bool { return containsAll(s, "THREADS ␣R", "1 / 1", "don't touch stored invoices") })
}

// AI-5: oriel.thread lists, replies to and resolves the session's threads from Lua.
func TestLuaThreads(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	answered(t, r.store, a)
	a.Emit(agent.TurnEnd{})
	lua := func(line string) {
		t.Helper()
		if err := r.do(func() error { return r.host.Run(context.Background(), "lua", registry.Args{"args": line}) }); err != nil {
			t.Fatal(err)
		}
	}
	lua(`oriel.notify("threads: " .. oriel.thread.list()[1].title .. " · " .. oriel.thread.list()[1].status)`)
	r.waitText("threads: Negative values round the wrong way · open")
	lua(`oriel.thread.reply("` + string(th.ID) + `", "from lua")`)
	if in := a.WaitSent(t); !strings.Contains(in.Text, "from lua") {
		t.Errorf("sent %q", in.Text)
	}
	lua(`oriel.thread.resolve("` + string(th.ID) + `")`)
	waitState(t, r.store, "the thread resolved", func(st core.State) bool {
		se, _ := st.Session("w")
		x, _ := se.Snapshot().Thread(th.ID)
		return x.Status == core.ThreadResolved
	})
}

// AI-9: the summary's tasks rail follows the agent's task tools, its stats the turn, and the title bar shows the
// plan's usage windows as the provider reports them.
func TestTasksRailStatsAndUsage(t *testing.T) {
	r, a, _ := agentSession(t)
	r.do(func() error { return r.host.Run(context.Background(), "requests.summary", nil) })
	r.waitText("No tasks yet")
	a.Call(t, "task_add", map[string]any{"subject": "Add RoundingMode type"})
	a.Call(t, "task_add", map[string]any{"subject": "Negative tie handling"})
	a.Call(t, "task_update", map[string]any{"id": "1", "status": "completed"})
	a.Call(t, "task_update", map[string]any{"id": "2", "status": "in_progress"})
	r.waitFrame("the tasks rail", func(s string) bool {
		return containsAll(s, "TASKS", "1/2", "Add RoundingMode type", "Negative tie handling", "SESSION", "CTX", "CACHE")
	})
	a.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "fake", Plan: "Fake Max", Windows: []core.UsageWindow{
		{Name: "5h window", Used: .38}, {Name: "weekly", Used: .71}}}})
	r.waitFrame("the usage windows", func(s string) bool { return containsAll(s, "5H", "38%", "WK", "71%") })
}

// A request's conversation follows its replies, but the wheel scrolls it back, and it stays there as more arrive.
func TestRequestScrollsBack(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	for i := range 30 {
		a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": fmt.Sprintf("Reply number %02d.", i)})
	}
	// The conversation draws every reply, clipped: one is in view when it's drawn inside the window.
	inView := func(f headless.Frame, text string) bool {
		op, ok := runIn(f, text)
		return ok && op.At.Y > 0 && op.At.Y < r.win.Size().Y
	}
	r.latest("the last reply in view", func(f headless.Frame) bool { return inView(f, "Reply number 29.") })
	r.scrollAt("Reply number 29.", -20000)
	r.latest("the first reply scrolled into view", func(f headless.Frame) bool { return inView(f, "Reply number 00.") })
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Reply number 30."})
	time.Sleep(200 * time.Millisecond)
	r.latest("the first reply still in view", func(f headless.Frame) bool { return inView(f, "Reply number 00.") })
}
