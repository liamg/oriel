package agentui_test

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
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
)

// requestsSession is agentSession with the Requests view open from the rail's key, ⌘⇧R.
func requestsSession(t *testing.T) (*apptest.Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	r, a, lw := apptest.AgentWindow(t)
	r.TypeKeys("<D-R>")
	r.WaitText("New request")
	return r, a, lw
}

// threadWith waits for the session's thread whose first comment is body.
func threadWith(t *testing.T, r *apptest.Window, body string) core.Thread {
	t.Helper()
	var th core.Thread
	r.UntilState("the thread "+body, func(st core.State) bool {
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
func projectDir(t *testing.T, r *apptest.Window) string {
	t.Helper()
	se, ok := r.Store.Session("w")
	if !ok {
		t.Fatal("no session w")
	}
	p, _ := se.Snapshot().Primary()
	return p.Dir
}

// newRequest writes a request in the composer and sends it with ⏎, returning when the agent has it and how long that
// took from the key.
func newRequest(t *testing.T, r *apptest.Window, a *agenttest.FakeSession, body string) (agent.Input, time.Duration) {
	t.Helper()
	r.ClickText("New request")
	r.WaitText("Write a request")
	r.TypeText(body)
	r.WaitText(body)
	start := time.Now()
	r.TypeKeys("<CR>")
	in := a.WaitSent(t)
	return in, time.Since(start)
}

// A comment reaches the agent within 1 s; the fake agent is idle, so the host hands it over at once.
func TestACommentReachesTheAgentWithinASecond(t *testing.T) {
	r, a, _ := requestsSession(t)
	in, took := newRequest(t, r, a, "Negative values round the wrong way")
	if took > time.Second {
		t.Errorf("the comment took %v to reach the agent", took)
	}
	if !strings.Contains(in.Text, "Negative values round the wrong way") {
		t.Errorf("the agent got %q", in.Text)
	}
	r.WaitFrame("the request, working", func(s string) bool {
		return apptest.HasAll(s, "WORKING", "Negative values round the wrong way", "FAKE · WORKING")
	})
}

// The request the agent is on ends in its working line, moving dots read out as who's working, until it answers.
func TestABusyRequestShowsTheAgentWorking(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	a.Emit(agent.TurnStart{})
	r.WaitFrame("the working line", func(s string) bool {
		return apptest.HasAll(s, "Negative values round the wrong way", "Fake is working")
	})
	r.WaitAccess("the dots read out", func(r *apptest.Window) bool { return r.Access("Fake is working") != nil })
	if n := r.Access("Fake is working"); n.Role != "status" {
		t.Errorf("the dots read as %+v", n)
	}
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Fixed."})
	a.Emit(agent.TurnEnd{})
	r.WaitText("Fixed.")
	r.WaitGone("Fake is working")
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

// Replies render as markdown with no activity or run chips (those stay in the console, where a run opens); only a
// "tests pass" with no run behind it is flagged.
func TestRepliesRenderAsMarkdownFlaggingUnbackedClaims(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	dir := projectDir(t, r)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.ToolCall{ID: "e1", Tool: "Edit", Input: rig.ToolInput(map[string]string{"file_path": filepath.Join(dir, "internal/pricing/round.go")})})
	a.Emit(agent.ToolResult{ID: "e1", Diff: &core.FileDiff{Path: filepath.Join(dir, "internal/pricing/round.go"), Added: 7, Removed: 1}})
	a.Emit(agent.ToolCall{ID: "b1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "go test ./internal/pricing/"})})
	a.Emit(agent.ToolResult{ID: "b1", Output: "--- PASS: TestRoundHalfEven (0.00s)\nok  \tpricing\t0.01s\n"})
	r.UntilState("the run", func(st core.State) bool {
		s, _ := st.Session("w")
		return s != nil && len(s.Snapshot().Runs) == 1
	})
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Right: `Floor` rounds toward -∞.\n\n- made the negative cases table-driven\n- checked the `RoundMinor` callers\n\nThe tests pass."})
	f := r.Latest("the reply as markdown", func(f headless.Frame) bool {
		return apptest.HasAll(f.Text(), "Right:", "rounds toward -∞.", "•", "made the negative cases table-driven", "The tests pass.")
	})
	for _, not := range []string{"ACTIVITY", "2 passed", "says tests pass"} {
		if strings.Contains(f.Text(), not) {
			t.Errorf("the backed reply shows %q", not)
		}
	}
	if op, ok := runIn(f, "Floor"); !ok || op.Run.Font.Family != typeset.Mono {
		t.Errorf("inline code isn't a mono run of its own: %+v", op.Run)
	}
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Tidied the table; tests pass."})
	r.WaitText("⚠ says tests pass, but no passing test run backs it")

	r.TypeKeys("<D-A>")
	r.WaitText("SESSION STATS")
	r.WaitFrame("the console's evidence on the test run", func(s string) bool { return apptest.HasAll(s, "go test ./internal/pricing/", "2 passed") })
	r.ClickText("2 passed")
	r.WaitFrame("the run it links to", func(s string) bool {
		return apptest.HasAll(s, "go test ./internal/pricing/", "--- PASS: TestRoundHalfEven (0.00s)", "BY AGENT")
	})

	r.Do(func() error { return r.Host.Run(context.Background(), "tests.open", nil) })
	r.WaitFrame("the agent's run in the Tests panel", func(s string) bool { return apptest.HasAll(s, "TESTS", "AGENT", "2 passed") })
}

// Urgent interrupts the turn, and btw is answered by a fork, outside the conversation.
func TestUrgentInterruptsAndBtwAsksAFork(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	threadWith(t, r, "Negative values round the wrong way")
	a.Emit(agent.TurnStart{})
	r.WaitText("FAKE · WORKING")
	r.ClickText("URGENT · INTERRUPTS TURN")
	r.ClickText("Reply, or ask more")
	r.TypeText("negative cases first")
	r.TypeKeys("<CR>")
	got := a.WaitSent(t)
	if a.Interrupts() != 1 || !strings.Contains(got.Text, "negative cases first") || !strings.Contains(got.Text, `urgent="true"`) {
		t.Errorf("interrupted %d times; sent %q", a.Interrupts(), got.Text)
	}
	rig.Answered(t, r.Store, a)
	a.Emit(agent.TurnEnd{})

	r.ClickText("BTW")
	r.ClickText("Reply, or ask more")
	r.TypeText("why decimal and not big.Rat?")
	r.TypeKeys("<CR>")
	r.WaitText("fork says:")
	a.NoneSent(t)
}

// A thread opens between the lines from the editor, reaches the agent anchored there, shows the agent's reply
// in place, and takes a reply of its own.
func TestInlineThreadsInTheEditor(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.WaitText("floor := scaled.Floor()")
	r.TypeKeys("4G")
	r.WaitFrame("the cursor on line 4", func(s string) bool { return strings.Contains(s, "\n4:") })
	r.Do(func() error { return r.Host.Run(context.Background(), "thread.new", nil) })
	r.WaitText("New thread")
	r.TypeText("Floor goes the wrong way for negatives")
	r.WaitText("Floor goes the wrong way for negatives")
	r.TypeKeys("<CR>")
	in := a.WaitSent(t)
	if !strings.Contains(in.Text, "round.go:4") || !strings.Contains(in.Text, "Floor goes the wrong way for negatives") {
		t.Errorf("the agent got %q", in.Text)
	}
	th := threadWith(t, r, "Floor goes the wrong way for negatives")
	r.WaitFrame("the thread under its line", func(s string) bool {
		return apptest.HasAll(s, "Thread", "L4", "FAKE · WORKING", "Floor goes the wrong way for negatives")
	})
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Switching to `Truncate` with sign handling."})
	a.Emit(agent.TurnEnd{})
	r.WaitText("with sign handling.")
	r.ClickText("Reply…")
	r.TypeText("and cover -2.5")
	r.TypeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "and cover -2.5") {
		t.Errorf("the inline reply sent %q", in.Text)
	}
}

// ⌃a in a picker attaches the item to the request being written, and ␣r picks a thread.
func TestPickersAttachAndPickThreads(t *testing.T) {
	r, a, _ := requestsSession(t)
	os.WriteFile(filepath.Join(projectDir(t, r), "invoice.go"), []byte("package billing\n"), 0o644)
	r.ClickText("New request")
	r.WaitText("Write a request")
	r.ClickText("@ FILE")
	r.TypeText("invoice")
	r.WaitText("invoice.go")
	r.TypeKeys("<C-a>")
	r.WaitText("@invoice.go")
	r.ClickText("Write a request")
	r.TypeText("don't touch stored invoices")
	r.TypeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "invoice.go") || !strings.Contains(in.Text, "don't touch stored invoices") {
		t.Errorf("sent %q", in.Text)
	}
	threadWith(t, r, "don't touch stored invoices\n\n@invoice.go")

	r.Do(func() error {
		return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": filepath.Join(projectDir(t, r), "invoice.go")})
	})
	r.WaitText("package billing")
	r.TypeKeys("<Space>r")
	r.WaitFrame("the threads picker", func(s string) bool { return apptest.HasAll(s, "THREADS ␣R", "1 / 1", "don't touch stored invoices") })
}

// oriel.thread lists, replies to and resolves the session's threads from Lua.
func TestLuaThreads(t *testing.T) {
	r, a, _ := requestsSession(t)
	newRequest(t, r, a, "Negative values round the wrong way")
	th := threadWith(t, r, "Negative values round the wrong way")
	rig.Answered(t, r.Store, a)
	a.Emit(agent.TurnEnd{})
	lua := func(line string) {
		t.Helper()
		if err := r.Do(func() error { return r.Host.Run(context.Background(), "lua", registry.Args{"args": line}) }); err != nil {
			t.Fatal(err)
		}
	}
	lua(`oriel.notify("threads: " .. oriel.thread.list()[1].title .. " · " .. oriel.thread.list()[1].status)`)
	r.WaitText("threads: Negative values round the wrong way · open")
	lua(`oriel.thread.reply("` + string(th.ID) + `", "from lua")`)
	if in := a.WaitSent(t); !strings.Contains(in.Text, "from lua") {
		t.Errorf("sent %q", in.Text)
	}
	lua(`oriel.thread.resolve("` + string(th.ID) + `")`)
	r.UntilState("the thread resolved", func(st core.State) bool {
		se, _ := st.Session("w")
		x, _ := se.Snapshot().Thread(th.ID)
		return x.Status == core.ThreadResolved
	})
}

// The summary's tasks rail follows the agent's task tools, its stats the turn, and the title bar shows the
// plan's usage windows as the provider reports them.
func TestTasksRailStatsAndUsage(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Do(func() error { return r.Host.Run(context.Background(), "requests.summary", nil) })
	r.WaitText("No tasks yet")
	a.Call(t, "task_add", map[string]any{"subject": "Add RoundingMode type"})
	a.Call(t, "task_add", map[string]any{"subject": "Negative tie handling"})
	a.Call(t, "task_update", map[string]any{"id": "1", "status": "completed"})
	a.Call(t, "task_update", map[string]any{"id": "2", "status": "in_progress"})
	r.WaitFrame("the tasks rail", func(s string) bool {
		return apptest.HasAll(s, "TASKS", "1/2", "Add RoundingMode type", "Negative tie handling", "SESSION", "CTX", "CACHE")
	})
	a.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "fake", Plan: "Fake Max", Windows: []core.UsageWindow{
		{Name: "5h window", Used: .38}, {Name: "weekly", Used: .71}}}})
	r.WaitFrame("the usage windows", func(s string) bool { return apptest.HasAll(s, "5H", "38%", "WK", "71%") })
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
		return ok && op.At.Y > 0 && op.At.Y < r.Win.Size().Y
	}
	r.Latest("the last reply in view", func(f headless.Frame) bool { return inView(f, "Reply number 29.") })
	r.ScrollAt("Reply number 29.", -20000)
	r.Latest("the first reply scrolled into view", func(f headless.Frame) bool { return inView(f, "Reply number 00.") })
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Reply number 30."})
	time.Sleep(200 * time.Millisecond)
	r.Latest("the first reply still in view", func(f headless.Frame) bool { return inView(f, "Reply number 00.") })
}
