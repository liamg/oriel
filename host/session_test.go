package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/host/agentrun"
	"github.com/liamg/oriel/watch"
)

// workSession starts a work session with the fake agent, and returns it once the agent has its first batch.
func (r *rig) workSession(prompt string) (core.SessionID, *agenttest.FakeSession) {
	r.t.Helper()
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: prompt, Provider: "fake", Model: "m1", Effort: "high",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	return id, r.agent()
}

// idleWithAComment is a work session whose first turn has ended, and the comment to send it.
func idleWithAComment(t *testing.T) (*rig, core.SessionID, *agenttest.FakeSession, CommentReq) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	first := a.WaitSent(t)
	if !strings.HasPrefix(first.Text, BatchLead) || !strings.Contains(first.Text, "Negative values round the wrong way") {
		t.Fatalf("first batch = %q", first.Text)
	}
	a.Emit(agent.TurnStart{})
	answered(r, id, a)
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn to end", func(se core.SessionState) bool { return !se.Agent.Working && se.Agent.Stats.Turns == 1 })
	return r, id, a, CommentReq{Session: id, Anchor: &core.Anchor{File: "round.go", Line: 3, Text: "func Round() {}"}, Body: "what about -2.5?"}
}

// A comment to an idle agent reaches it at once (how soon is TestACommentReachesTheAgentBudget).
func TestACommentReachesTheAgent(t *testing.T) {
	r, id, a, req := idleWithAComment(t)
	tid, err := r.w.Comment(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := a.WaitSent(t)
	for _, want := range []string{`where="round.go:3"`, `<anchor>func Round() {}</anchor>`, `new="true"`, "what about -2.5?", string(tid)} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("batch lacks %q:\n%s", want, got.Text)
		}
	}
	th, _ := r.session(id).Thread(tid)
	if !th.Busy || !th.Comments[0].Delivered {
		t.Errorf("thread after delivery = %+v", th)
	}
	r.eventually(id, "working", func(se core.SessionState) bool { return se.Status == core.StatusWorking })
}

// Mid-turn a comment waits for the turn's end; an urgent one interrupts it.
func TestCommentsWaitForTheTurnUnlessUrgent(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "later please"}); err != nil {
		t.Fatal(err)
	}
	r.noneSent(a)
	a.Emit(agent.TurnEnd{})
	if got := a.WaitSent(t); !strings.Contains(got.Text, "later please") {
		t.Errorf("after the turn: %q", got.Text)
	}
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the next turn", func(se core.SessionState) bool { return se.Agent.Working })
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "stop, this first", Urgent: true}); err != nil {
		t.Fatal(err)
	}
	got := a.WaitSent(t) // the fake ends the turn when interrupted
	if a.Interrupts() != 1 || !strings.Contains(got.Text, `urgent="true"`) {
		t.Errorf("interrupted %d times; batch %q", a.Interrupts(), got.Text)
	}
}

func TestTheAgentRepliesWithEvidence(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix rounding")
	a.WaitSent(t)
	tid := r.session(id).Threads[0].ID
	a.Emit(agent.TurnStart{})
	edit, _ := json.Marshal(map[string]string{"file_path": r.session(id).Projects[0].Dir + "/round.go"})
	a.Emit(agent.ToolCall{ID: "c1", Tool: "Edit", Input: edit})
	a.Emit(agent.ToolResult{ID: "c1", Output: "ok", Diff: &core.FileDiff{Path: "round.go", Added: 7, Removed: 1}})
	test, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	a.Emit(agent.ToolCall{ID: "c2", Tool: "Bash", Input: test})
	a.Emit(agent.ToolResult{ID: "c2", Output: "--- PASS: TestRound (0.00s)\nok  \tpricing\t0.01s\n"})
	se := r.eventually(id, "the test run", func(se core.SessionState) bool { return len(se.Runs) == 1 })
	run := se.Runs[0]
	if !run.OK || run.Passed != 2 || run.Command != "go test ./..." || run.Thread != tid || run.Item != "c2" {
		t.Errorf("run = %+v", run)
	}
	out := a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Fixed: all tests pass."})
	if out != "replied" {
		t.Errorf("reply said %q", out)
	}
	th, _ := r.session(id).Thread(tid)
	c, _ := th.Last()
	if c.Author != core.AuthorAgent || c.Model != "m1 · high" || c.Unbacked != "" {
		t.Fatalf("reply = %+v", c)
	}
	if len(c.Evidence) != 1 || c.Evidence[0].Ref != run.ID || !c.Evidence[0].OK {
		t.Errorf("evidence = %+v", c.Evidence)
	}
	if len(th.Touched) != 1 || th.Touched[0].Added != 7 {
		t.Errorf("touched = %+v", th.Touched)
	}
	a.Emit(agent.TurnEnd{})
	// The status follows the turn's end in an event of its own: wait for it, not for the agent stopping.
	r.eventually(id, "needs you", func(se core.SessionState) bool { return !se.Agent.Working && se.Status == core.StatusNeedsYou })
}

// An agent that answers its first turn before its start has returned answers comments it was given: they count as
// delivered from when it's handed them, not from when its start returns.
func TestAnAnswerBeforeTheStartReturnsIsTaken(t *testing.T) {
	r := newRig(t)
	hold := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(hold) })
	r.fake.HoldStart = hold
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	if out, err := a.CallErr("reply", map[string]any{"thread": tid, "body": "On it."}); err != nil {
		t.Errorf("the answer to its first turn was refused: %v", err)
	} else if out != "replied" {
		t.Errorf("reply said %q", out)
	}
	release.Do(func() { close(hold) })
	r.eventually(id, "the agent running", func(se core.SessionState) bool { return se.Agent.Running })
}

// A "tests pass" with no run behind it is flagged; one citing a run links it.
func TestClaimsWithoutEvidenceAreFlagged(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	if out := a.Call(t, "reply", map[string]any{"thread": tid, "body": "Done, tests pass."}); !strings.Contains(out, "no passing test run") {
		t.Errorf("the agent wasn't told: %q", out)
	}
	th, _ := r.session(id).Thread(core.ThreadID(tid))
	if c, _ := th.Last(); c.Unbacked == "" {
		t.Error("an unbacked claim wasn't flagged")
	}
	if err := r.w.emit(core.TestRunRecorded{Session: id, Run: core.TestRun{ID: "beef01", Command: "go test", Passed: 3, OK: true}}); err != nil {
		t.Fatal(err)
	}
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Re-ran: the suite passes, run:beef01."})
	th, _ = r.session(id).Thread(core.ThreadID(tid))
	if c, _ := th.Last(); c.Unbacked != "" || len(c.Evidence) != 1 || c.Evidence[0].Ref != "beef01" {
		t.Errorf("a cited run: %+v", c)
	}
}

// A turn's evidence is the run it ended on, not the failed ones on the way.
func TestEvidenceIsTheLastRun(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	a.Emit(agent.TurnStart{})
	for i, out := range []string{"--- FAIL: TestA (0.00s)\nFAIL\tpricing\t0.01s\n", "--- FAIL: TestA (0.00s)\nFAIL\tpricing\t0.01s\n", "--- PASS: TestA (0.00s)\nok  \tpricing\t0.01s\n"} {
		call := fmt.Sprint("c", i)
		test, _ := json.Marshal(map[string]string{"command": "go test ./..."})
		a.Emit(agent.ToolCall{ID: call, Tool: "Bash", Input: test})
		a.Emit(agent.ToolResult{ID: call, Output: out})
		r.eventually(id, "run "+call, func(se core.SessionState) bool { return len(se.Runs) == i+1 })
	}
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Fixed: tests pass."})
	th, _ := r.session(id).Thread(core.ThreadID(tid))
	c, _ := th.Last()
	if len(c.Evidence) != 1 || !c.Evidence[0].OK || c.Evidence[0].Ref != r.session(id).Runs[2].ID || c.Unbacked != "" {
		t.Errorf("evidence = %+v, unbacked %q", c.Evidence, c.Unbacked)
	}
}

// A test_run call's transcript item links to the run it recorded, so the console's block can show it.
func TestTestRunCallsLinkTheirRun(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	if err := r.w.emit(core.TestRunRecorded{Session: id, Run: core.TestRun{ID: "beef01", Command: "go test ./...", Passed: 3, OK: true}}); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "c9", Tool: "mcp__oriel__test_run", Input: json.RawMessage(`{}`)})
	a.Emit(agent.ToolResult{ID: "c9", Output: "run:beef01 passed — 3 passed, 0 failed, 0 skipped"})
	r.eventually(id, "the run linked to its call", func(se core.SessionState) bool {
		run, ok := se.Run("beef01")
		return ok && run.Item == "c9"
	})
}

func TestTasksFollowTheirThreads(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Do two things")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	a.Call(t, "task_add", map[string]any{"subject": "Negative tie handling", "thread": tid[:4]})
	a.Call(t, "task_add", map[string]any{"subject": "Commit + PR draft"})
	a.Call(t, "task_update", map[string]any{"id": "1", "status": "in_progress"})
	if tasks := r.session(id).Tasks; len(tasks) != 2 || tasks[0].Status != core.TaskInProgress || string(tasks[0].Thread) != tid {
		t.Fatalf("tasks = %+v", tasks)
	}
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "done", "resolve": true})
	tasks := r.session(id).Tasks
	if tasks[0].Status != core.TaskCompleted || tasks[1].Status != core.TaskPending {
		t.Errorf("resolving the thread should complete its task only: %+v", tasks)
	}
	if !strings.Contains(a.Call(t, "tasks", nil), "Commit + PR draft") {
		t.Error("tasks tool doesn't list them")
	}
}

// Permissions, three layers: rules and the app's hooks answer first; the user only when neither does.
func TestPermissionLayers(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Run things")
	a.WaitSent(t)
	cmd, _ := json.Marshal(map[string]string{"command": "go test ./internal/api/... -run TestUsage"})
	ans := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Summary: "go test ./internal/api/...", Input: cmd})
	se := r.eventually(id, "the permission card", func(se core.SessionState) bool {
		return len(se.Asks) == 1 && se.Status == core.StatusPermission
	})
	if se.Asks[0].Rule != "Bash(go test:*)" {
		t.Fatalf("ask = %+v, status %s", se.Asks, se.Status)
	}
	if err := r.w.Answer(AnswerReq{Session: id, Ask: "p1", Decision: "always"}); err != nil {
		t.Fatal(err)
	}
	if d := <-ans; !d.Allow || !d.Always {
		t.Errorf("decision = %+v", d)
	}
	se = r.session(id)
	if len(se.Asks) != 0 || len(se.Rules) != 1 {
		t.Fatalf("after Always: asks %+v, rules %+v", se.Asks, se.Rules)
	}
	// The rule answers the next one at once.
	cmd2, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	if d := <-a.Ask(agent.Permission{ID: "p2", Tool: "Bash", Input: cmd2}); !d.Allow {
		t.Error("the rule didn't allow go test")
	}
	// A deny carries the user's note back to the agent.
	rm, _ := json.Marshal(map[string]string{"command": "rm -rf build"})
	ans = a.Ask(agent.Permission{ID: "p3", Tool: "Bash", Input: rm})
	r.eventually(id, "the card", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	r.w.Answer(AnswerReq{Session: id, Ask: "p3", Decision: "deny", Note: "use the Makefile target"})
	if d := <-ans; d.Allow || !strings.Contains(d.Note, "use the Makefile target") {
		t.Errorf("deny = %+v", d)
	}
	// The app's policy hooks (layer 2) answer without a card.
	r.w.SetApp(App{Decide: func(_ context.Context, d Decide) Verdict {
		if strings.Contains(d.Ask.Input, "curl") {
			return Verdict{Decision: "deny", Note: "no network from Bash", By: "init.lua"}
		}
		return Verdict{}
	}})
	curl, _ := json.Marshal(map[string]string{"command": "curl example.com"})
	if d := <-a.Ask(agent.Permission{ID: "p4", Tool: "Bash", Input: curl}); d.Allow || d.Note != "no network from Bash" {
		t.Errorf("policy hook = %+v", d)
	}
	if len(r.session(id).Asks) != 0 {
		t.Error("a hook's answer still showed a card")
	}
}

// An MCP call (a claude.ai connector's here) is put to Oriel by the agent's provider whatever its mode, and asks
// through the same layers: a card naming the tool, "always" making a rule for the tool that answers its next call
// at once, and nothing for another tool of the same server.
func TestAnMCPCallAsksThroughTheSessionsLayers(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Read the docs")
	a.WaitSent(t)
	const tool = "mcp__claude_ai_Claude_Docs__guide"
	in := json.RawMessage(`{"items":["topic.index"]}`)
	ans := a.Ask(agent.Permission{ID: "m1", Tool: tool, Summary: `{"items":["topic.index"]}`, Input: in})
	se := r.eventually(id, "the card", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	if ask := se.Asks[0]; ask.Kind != core.AskPermission || ask.Tool != tool || ask.Rule != tool {
		t.Fatalf("ask = %+v", ask)
	}
	if err := r.w.Answer(AnswerReq{Session: id, Ask: "m1", Decision: "always"}); err != nil {
		t.Fatal(err)
	}
	if d := <-ans; !d.Allow || !d.Always {
		t.Errorf("always = %+v", d)
	}
	if d := <-a.Ask(agent.Permission{ID: "m2", Tool: tool, Input: in}); !d.Allow {
		t.Errorf("the rule didn't answer the next call: %+v", d)
	}
	ans = a.Ask(agent.Permission{ID: "m3", Tool: "mcp__claude_ai_Claude_Docs__delete", Input: json.RawMessage(`{}`)})
	r.eventually(id, "a card for the other tool", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	r.w.Answer(AnswerReq{Session: id, Ask: "m3", Decision: "deny"})
	if d := <-ans; d.Allow {
		t.Errorf("the other tool was allowed: %+v", d)
	}
}

// The app asks for a session's agent (a tool that asks first) through the same layers: a card, "always" making
// a rule that answers the next at once, and a deny's note.
func TestTheAppAsksThroughTheSessionsLayers(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Debug it")
	a.WaitSent(t)
	ask := core.Ask{Tool: "mcp__oriel__debug_step", Summary: "step over in main.main", Rule: "mcp__oriel__debug_step"}
	got := make(chan Verdict, 1)
	go func() {
		v, err := r.w.Ask(context.Background(), AskReq{Session: id, Ask: ask})
		if err != nil {
			t.Error(err)
		}
		got <- v
	}()
	se := r.eventually(id, "the card", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	if k := se.Asks[0].Kind; k != core.AskPermission || se.Asks[0].Summary != ask.Summary {
		t.Fatalf("ask = %+v", se.Asks[0])
	}
	r.w.Answer(AnswerReq{Session: id, Ask: se.Asks[0].ID, Decision: "always"})
	if v := <-got; v.Decision != "allow" || v.By != "the user" {
		t.Errorf("always = %+v", v)
	}
	if v, err := r.w.Ask(context.Background(), AskReq{Session: id, Ask: ask}); err != nil || v.Decision != "allow" || !strings.HasPrefix(v.By, "rule ") {
		t.Errorf("after always: %+v %v", v, err)
	}
	go func() {
		v, _ := r.w.Ask(context.Background(), AskReq{Session: id, Ask: core.Ask{Tool: "mcp__oriel__debug_eval", Summary: "evaluate os.Exit(1)"}})
		got <- v
	}()
	se = r.eventually(id, "the second card", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	r.w.Answer(AnswerReq{Session: id, Ask: se.Asks[0].ID, Decision: "deny", Note: "not that"})
	if v := <-got; v.Decision != "deny" || v.Note != "not that" {
		t.Errorf("deny = %+v", v)
	}
	if _, err := r.w.Ask(context.Background(), AskReq{Session: "nope", Ask: ask}); err == nil {
		t.Error("asked for a session that isn't running")
	}
}

func TestQuestionsWaitForTheUser(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Ask")
	a.WaitSent(t)
	ch := a.Expect("q1")
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: "Which colour?", Options: []string{"red", "blue"}}}})
	// The status follows the ask in an event of its own.
	se := r.eventually(id, "the question", func(se core.SessionState) bool { return len(se.Asks) == 1 && se.Status == core.StatusNeedsYou })
	if se.Asks[0].Kind != core.AskQuestion {
		t.Fatalf("status %s, asks %+v", se.Status, se.Asks)
	}
	r.w.Answer(AnswerReq{Session: id, Ask: "q1", Decision: "answer", Answers: map[string]string{"Which colour?": "blue"}})
	if d := <-ch; d.Answers["Which colour?"] != "blue" {
		t.Errorf("answers = %+v", d.Answers)
	}
}

// A question is asked in a thread: the one the agent names; with none named (Claude's own question tool), the one it's
// working on, else the session's request, else a new general thread of its own.
func TestAQuestionIsAskedInAThread(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	req := r.session(id).Threads[0].ID
	other, err := r.w.Comment(context.Background(), CommentReq{Session: id, Anchor: &core.Anchor{File: "round.go", Line: 3}, Body: "and zero?"})
	if err != nil {
		t.Fatal(err)
	}

	// Unnamed, it's asked in the thread the agent was handed and is on: the request, not the comment waiting behind it.
	ch := a.Expect("q1")
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: "Half to even?", Options: []string{"yes", "no"}}}})
	se := r.eventually(id, "the first question", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	if se.Asks[0].Thread != req {
		t.Errorf("an unnamed question was asked in %q, want the active thread %q", se.Asks[0].Thread, req)
	}
	r.w.Answer(AnswerReq{Session: id, Ask: "q1", Answers: map[string]string{"Half to even?": "yes"}})
	<-ch

	// Named, through Oriel's own tool, it's asked there, and the tool answers with the user's choice.
	got := make(chan string, 1)
	go func() {
		out, err := a.CallErr("ask", map[string]any{"thread": string(other), "questions": []map[string]any{{"question": "Which zero?", "options": []string{"+0", "-0"}}}})
		if err != nil {
			out = "error: " + err.Error()
		}
		got <- out
	}()
	se = r.eventually(id, "the named question", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	if se.Asks[0].Thread != other || se.Asks[0].Kind != core.AskQuestion || se.Asks[0].Questions[0].Question != "Which zero?" {
		t.Errorf("the ask tool asked %+v, want a question in %q", se.Asks[0], other)
	}
	r.w.Answer(AnswerReq{Session: id, Ask: se.Asks[0].ID, Answers: map[string]string{"Which zero?": "-0"}})
	if out := <-got; !strings.Contains(out, "Which zero?") || !strings.Contains(out, "-0") {
		t.Errorf("the ask tool answered %q", out)
	}
	if _, err := a.CallErr("ask", map[string]any{"questions": []map[string]any{{"question": "Where?"}}}); err == nil {
		t.Error("the ask tool asked with no thread")
	}
	if _, err := a.CallErr("ask", map[string]any{"thread": "nope", "questions": []map[string]any{{"question": "Where?"}}}); err == nil {
		t.Error("the ask tool asked in a thread that isn't there")
	}
}

// A question with no thread named, from an agent on no thread, is asked in the session's request.
func TestAQuestionOffAThreadIsAskedInTheRequest(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	req := r.session(id).Threads[0].ID
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Anchor: &core.Anchor{File: "round.go", Line: 3}, Body: "and zero?"}); err != nil {
		t.Fatal(err)
	}
	a.Call(t, "reply", map[string]any{"thread": string(req), "body": "Which way should they round?"})
	r.eventually(id, "the request handed back", func(se core.SessionState) bool { th, _ := se.Thread(req); return !th.Busy })
	ch := a.Expect("q1")
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: "Half to even?"}}})
	se := r.eventually(id, "the question", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	if se.Asks[0].Thread != req {
		t.Errorf("the question was asked in %q, want the request %q", se.Asks[0].Thread, req)
	}
	r.w.Answer(AnswerReq{Session: id, Ask: "q1", Answers: map[string]string{"Half to even?": "yes"}})
	<-ch
}

// A question from an agent with no thread to ask in opens a general thread for it.
func TestAQuestionWithNoThreadOpensOne(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Provider: "fake", Model: "m1", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	a := r.agent()
	ch := a.Expect("q1")
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: "What should it do?"}}})
	se := r.eventually(id, "the question", func(se core.SessionState) bool { return len(se.Asks) == 1 })
	th, ok := se.Thread(se.Asks[0].Thread)
	if !ok || th.Anchor != nil || !th.Open() {
		t.Fatalf("the question is in thread %q (%+v), want an open general one", se.Asks[0].Thread, th)
	}
	r.w.Answer(AnswerReq{Session: id, Ask: "q1", Answers: map[string]string{"What should it do?": "round"}})
	<-ch
	// The thread was the question's own: it keeps the answer, and is resolved.
	se = r.eventually(id, "the question's thread resolved", func(se core.SessionState) bool {
		t, _ := se.Thread(th.ID)
		return t.Status == core.ThreadResolved
	})
	t2, _ := se.Thread(th.ID)
	if last, _ := t2.Last(); last.Author != core.AuthorYou || last.Body != "What should it do? → round" || !last.Delivered {
		t.Errorf("the thread ends %+v, want your answer, given to the agent", last)
	}
}

// A btw is answered by a fork, and never joins the conversation.
func TestBtwIsAnsweredByAFork(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	r.eventually(id, "the agent running", func(se core.SessionState) bool { return se.Agent.Running }) // its first turn is sent as it launches
	tid, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "why decimal and not big.Rat?", Btw: true})
	if err != nil {
		t.Fatal(err)
	}
	se := r.eventually(id, "the fork's answer", func(se core.SessionState) bool {
		th, _ := se.Thread(tid)
		return len(th.Comments) == 2
	})
	th, _ := se.Thread(tid)
	if c := th.Comments[1]; c.Author != core.AuthorAgent || !c.Btw || !strings.HasPrefix(c.Body, "fork says:") {
		t.Errorf("answer = %+v", c)
	}
	r.noneSent(a)
}

// A fork asked with a context runs on it: its deadline is the fork's, and one already ended asks nothing.
func TestAForkRunsOnTheAskersContext(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	r.eventually(id, "the agent running", func(se core.SessionState) bool { return se.Agent.Running })
	by := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), by)
	defer cancel()
	if _, err := r.w.Fork(ctx, id, "why?"); err != nil {
		t.Fatal(err)
	}
	if got := a.ForkDeadline(); !got.Equal(by) {
		t.Errorf("the fork's deadline = %v, want the asker's %v", got, by)
	}
	cancel()
	if _, err := r.w.Fork(ctx, id, "why?"); !errors.Is(err, context.Canceled) {
		t.Errorf("a fork on an ended context = %v, want it cancelled", err)
	}
}

// Pin as note is a thread opened resolved; it stays with the user and never reaches the agent.
func TestAThreadOpenedResolvedIsANote(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	tid, err := r.w.Comment(context.Background(), CommentReq{Session: id, Anchor: &core.Anchor{File: "round.go", Line: 3},
		Title: "Explain: func Round", Body: "Rounds half to even.", Status: core.ThreadResolved})
	if err != nil {
		t.Fatal(err)
	}
	if th, _ := r.session(id).Thread(tid); th.Status != core.ThreadResolved || th.Busy {
		t.Errorf("the note = %+v", th)
	}
	r.noneSent(a)
}

func TestConsoleMessagesQueueUntilTheTurnEnds(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	if err := r.w.Send(SendReq{Session: id, Text: "also check routes.go"}); err != nil {
		t.Fatal(err)
	}
	if q := r.session(id).Agent.Queue; len(q) != 1 {
		t.Fatalf("queue = %v", q)
	}
	r.noneSent(a)
	a.Emit(agent.TurnEnd{})
	if got := a.WaitSent(t); got.Text != "also check routes.go" {
		t.Errorf("sent %q", got.Text)
	}
	if q := r.session(id).Agent.Queue; len(q) != 0 {
		t.Errorf("queue after = %v", q)
	}
}

// After a restart nothing runs; resuming starts the agent by its own id, so the conversation carries on.
func TestResumeAfterRestartUsesTheAgentsID(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Session == a.ID() })
	r.restart()
	se := r.session(id)
	if se.Agent.Running || se.Agent.Session != a.ID() || len(se.Threads) != 1 {
		t.Fatalf("after a restart: %+v", se.Agent)
	}
	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.agent()
	if len(r.fake.Resumed()) != 1 || r.fake.Resumed()[0] != a.ID() {
		t.Errorf("resumed %v, want %s", r.fake.Resumed(), a.ID())
	}
	// The transcript's last word isn't the exit: it says the agent is back.
	r.eventually(id, "the restart in the transcript", func(se core.SessionState) bool {
		n := len(se.Transcript)
		return n > 0 && se.Transcript[n-1].Kind == core.ItemSystem && se.Transcript[n-1].Text == "The agent started again, resuming its conversation"
	})
}

func TestStartSpecCarriesThePackToolsAndGuards(t *testing.T) {
	r := newRig(t)
	_, a := r.workSession("Work")
	s := a.Spec
	if s.Pack.Name != "work" || s.Model != "m1" || s.Effort != "high" || s.Policy != agent.PolicyAsk {
		t.Errorf("spec = %+v", s)
	}
	if len(s.Protect) == 0 || s.Protect[0] != r.repo.Dir {
		t.Errorf("the user's checkout isn't protected: %v", s.Protect)
	}
	var names []string
	for _, tool := range s.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"reply", "thread", "resolve", "comment", "ask", "task_add", "task_update", "tasks", "projects", "commit_draft", "pr_draft", "test_run"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+want+" ") {
			t.Errorf("no %s tool in %v", want, names)
		}
	}
}

// The app offers tools of its own (LSP lookups), and calls go back to it.
func TestAppToolsReachTheAgent(t *testing.T) {
	r := newRig(t)
	r.w.SetApp(App{
		Tools: func(ToolsReq) []agent.Tool {
			return []agent.Tool{{Name: "lsp_definition", Description: "Where a symbol is defined.", ReadOnly: true,
				Schema: json.RawMessage(`{"type":"object"}`)}}
		},
		CallTool: func(_ context.Context, call ToolCall) (string, error) { return "round.go:3 " + call.Tool, nil }})
	_, a := r.workSession("Work")
	if got := a.Call(t, "lsp_definition", map[string]any{"symbol": "Round"}); got != "round.go:3 lsp_definition" {
		t.Errorf("app tool = %q", got)
	}
}

// An app tool an MCP server of the agent's own already covers (CoveredBy) isn't offered to it; one it doesn't reach is.
func TestAToolTheAgentsOwnMCPServerCoversIsntOffered(t *testing.T) {
	for _, c := range []struct {
		name      string
		installed []agent.MCPServer
		offered   bool
	}{
		{"none installed", nil, true},
		{"by its address", []agent.MCPServer{{Name: "tickets", Config: json.RawMessage(`{"type":"http","url":"https://mcp.linear.app/mcp"}`)}}, false},
		{"through mcp-remote", []agent.MCPServer{{Name: "lin", Config: json.RawMessage(`{"command":"npx","args":["-y","mcp-remote","https://mcp.linear.app/sse"]}`)}}, false},
		{"another server", []agent.MCPServer{{Name: "github", Config: json.RawMessage(`{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`)}}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.fake.MCP = c.installed
			r.w.SetApp(App{Tools: func(ToolsReq) []agent.Tool {
				return []agent.Tool{
					{Name: "linear_issue", Schema: json.RawMessage(`{"type":"object"}`), CoveredBy: []string{"mcp.linear.app"}},
					{Name: "lsp_definition", Schema: json.RawMessage(`{"type":"object"}`)},
				}
			}})
			_, a := r.workSession("Work")
			has := func(name string) bool {
				return slices.ContainsFunc(a.Spec.Tools, func(tl agent.Tool) bool { return tl.Name == name })
			}
			if has("linear_issue") != c.offered {
				t.Errorf("linear_issue offered %v, want %v", has("linear_issue"), c.offered)
			}
			if !has("lsp_definition") {
				t.Error("a tool nothing covers wasn't offered")
			}
		})
	}
}

// A turn that ends with background work in flight (a Claude Code Stop hook's background_tasks) isn't the end of the
// work: its threads stay busy and the session working until a turn ends with none, the one the work wakes.
func TestBackgroundWorkKeepsTheTurnsThreadsBusy(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	answered(r, id, a) // nothing owed: the next the agent is sent is the comment, not a carry-on
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the first turn to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	tid, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "run the suite"})
	if err != nil {
		t.Fatal(err)
	}
	if in := a.WaitSent(t); !strings.Contains(in.Text, "run the suite") {
		t.Fatalf("the agent was sent %q, want the comment", in.Text)
	}
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{Background: []core.BackgroundTask{{Kind: "shell", Description: "go test ./..."}}})
	se := r.eventually(id, "the turn to end, still working", func(se core.SessionState) bool {
		return se.Agent.Stats.Turns == 2 && se.Status == core.StatusWorking
	})
	if th, _ := se.Thread(tid); !th.Busy {
		t.Errorf("the thread stopped being busy with the suite still running")
	}
	if se.Status != core.StatusWorking || len(se.Agent.Background) != 1 {
		t.Errorf("status %s, background %+v", se.Status, se.Agent.Background)
	}
	var mu sync.Mutex
	var idle []core.Event
	stop := r.w.store.Subscribe(core.Filter{Session: id}, func(ev core.Event) {
		switch ev.(type) {
		case core.ThreadBusy, core.ThreadUpdated:
			mu.Lock()
			idle = append(idle, ev)
			mu.Unlock()
		}
	})
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the woken turn to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 3 })
	stop()
	se = r.session(id)
	if th, _ := se.Thread(tid); th.Busy || len(se.Agent.Background) != 0 {
		t.Errorf("after the background work: busy %v, background %+v", th.Busy, se.Agent.Background)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := (core.ThreadBusy{Session: id, Thread: tid}); len(idle) != 1 || idle[0] != want {
		t.Errorf("the thread went idle with %#v, want only %#v", idle, want)
	}
}

// What a turn leaves running that won't wake the agent (a dev server in a Codex background terminal) is shown, not
// waited on: the turn's threads go idle, so the agent is told to carry on with what it owes. Stopping one reaches the
// provider and takes it off the list, and the agent stopping clears it.
func TestRunningProcessesDontHoldTheTurn(t *testing.T) {
	r := newRig(t)
	r.fake.Has = &agent.Features{StopProcesses: true}
	id, a := r.workSession("start the dev server")
	a.WaitSent(t)
	var tid core.ThreadID
	r.eventually(id, "the agent to have the thread", func(se core.SessionState) bool {
		if len(se.Threads) == 0 || !se.Threads[0].Busy {
			return false
		}
		tid = se.Threads[0].ID
		return true
	})
	a.Emit(agent.TurnStart{})
	idle := make(chan core.Event, 8)
	stop := r.w.store.Subscribe(core.Filter{Session: id}, func(ev core.Event) {
		if b, ok := ev.(core.ThreadBusy); ok {
			idle <- b
		}
	})
	defer stop()
	running := []core.BackgroundTask{{ID: "1001", Kind: "terminal", Description: "npm run dev", PID: 4242}, {ID: "1002", Kind: "terminal", Description: "go run ./watch"}}
	a.Emit(agent.TurnEnd{Running: running})
	r.eventually(id, "the turn to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	select {
	case ev := <-idle:
		if want := (core.ThreadBusy{Session: id, Thread: tid}); ev != want {
			t.Errorf("the thread changed with %#v, want %#v", ev, want)
		}
	case <-time.After(waitTimeout):
		t.Fatal("the thread stayed busy while the dev server ran")
	}
	if in := a.WaitSent(t); in.Text == "" {
		t.Error("the agent wasn't told to carry on")
	}
	if se := r.session(id); !slices.Equal(se.Agent.Processes, running) || len(se.Agent.Background) != 0 {
		t.Errorf("processes %+v, background %+v", se.Agent.Processes, se.Agent.Background)
	}
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{Running: running})
	r.eventually(id, "the carry-on to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 2 })
	if err := r.w.StopProcess(context.Background(), id, "1001"); err != nil {
		t.Fatal(err)
	}
	if got := a.Stopped(); !slices.Equal(got, []string{"1001"}) {
		t.Errorf("the provider was asked to stop %v", got)
	}
	if got := r.session(id).Agent.Processes; !slices.Equal(got, running[1:]) {
		t.Errorf("after the stop, processes %+v", got)
	}
	if err := r.w.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.eventually(id, "the agent to stop", func(se core.SessionState) bool { return !se.Agent.Running })
	if got := r.session(id).Agent.Processes; len(got) != 0 {
		t.Errorf("a stopped agent still lists %+v", got)
	}
	if err := r.w.StopProcess(context.Background(), id, "1002"); err == nil {
		t.Error("a stopped agent stopped a process")
	}
}

// A provider that can't stop processes says so.
func TestStopProcessNeedsAProviderThatCan(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	a.WaitSent(t)
	if err := r.w.StopProcess(context.Background(), id, "1001"); err == nil {
		t.Error("a provider that can't stop processes stopped one")
	}
	if got := a.Stopped(); len(got) != 0 {
		t.Errorf("stopped %v", got)
	}
}

// Configure switches an idle agent's model and effort: it comes back on them with its conversation kept. Mid-turn
// it refuses, so the turn isn't cut off.
func TestConfigureSwitchesTheModel(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn to start", func(se core.SessionState) bool { return se.Agent.Working })
	if err := r.w.Configure(context.Background(), ConfigureReq{Session: id, Model: "m2"}); err == nil {
		t.Error("Configure switched the model mid-turn")
	}
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn to end", func(se core.SessionState) bool { return !se.Agent.Working })
	if err := r.w.Configure(context.Background(), ConfigureReq{Session: id, Model: "m2", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	b := r.agent()
	if !a.Closed() || b.Spec.Model != "m2" || b.Spec.Effort != "low" {
		t.Errorf("the agent came back on %q %q; the old one closed %v", b.Spec.Model, b.Spec.Effort, a.Closed())
	}
	if got := r.fake.Resumed(); len(got) != 1 || got[0] != a.ID() {
		t.Errorf("resumed %v, want %s", got, a.ID())
	}
	if se := r.session(id); se.Agent.Model != "m2" || se.Agent.Effort != "low" {
		t.Errorf("the session's agent: %+v", se.Agent)
	}
}

// An edit's title names its file however the provider's input does: file_path, path, or its first of files.
func TestEditTitlesNameTheFile(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Edit things")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	for i, in := range []string{`{"file_path":"a.go"}`, `{"path":"b.go"}`, `{"files":[{"path":"c.go","kind":"update"},{"path":"d.go"}]}`} {
		a.Emit(agent.ToolCall{ID: fmt.Sprint("e", i), Tool: "Edit", Input: json.RawMessage(in)})
	}
	se := r.eventually(id, "the edits", func(se core.SessionState) bool { return len(se.Transcript) >= 3 })
	var titles []string
	for _, it := range se.Transcript {
		if it.Kind == core.ItemTool {
			titles = append(titles, it.Title)
		}
	}
	if strings.Join(titles, "|") != "Edit a.go|Edit b.go|Edit c.go" {
		t.Errorf("titles = %q", titles)
	}
}

// Resuming a session whose agent runs but has stopped answering starts it again on its conversation, and hands it
// what waits: a request left queued behind a turn the old one never ended.
func TestResumeRestartsAnAgentThatStoppedAnswering(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "and the zero case"}); err != nil {
		t.Fatal(err)
	}
	r.noneSent(a)

	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.noneSent(a) // still answering: it carries on with its turn

	a.Quiet()
	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	again := r.agent()
	if !a.Closed() {
		t.Error("the quiet agent wasn't stopped")
	}
	if rs := r.fake.Resumed(); len(rs) == 0 || rs[len(rs)-1] != a.ID() {
		t.Errorf("it wasn't resumed on its conversation: %q", rs)
	}
	got := again.WaitSent(t)
	if !strings.Contains(got.Text, "and the zero case") {
		t.Errorf("the agent started again was handed %q", got.Text)
	}
}

// An agent that lost what only a restart brings back (an MCP server its CLI dropped) is started again on its
// conversation once its turn ends.
func TestAStaleAgentRestartsAfterItsTurn(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the turn", func(se core.SessionState) bool { return se.Agent.Working })
	a.Emit(agent.Stale{Why: "its CLI dropped oriel-gw"})
	a.Handled(t)
	r.settle()
	if a.Closed() {
		t.Fatal("restarted mid-turn")
	}
	answered(r, id, a)
	a.Emit(agent.TurnEnd{})
	again := r.agent()
	if !a.Closed() || again == a {
		t.Error("the stale agent wasn't started again")
	}
	r.eventually(id, "running again", func(se core.SessionState) bool { return se.Agent.Running && !se.Agent.Working })
}

// Every reply in a turn is backed by the turn's runs, not only the first; and a run that passed tests backs a claim
// though other packages in it failed.
func TestEvidenceBacksEveryReplyOfTheTurn(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	other, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "and this"})
	if err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the comment handed to the agent", func(se core.SessionState) bool {
		th, _ := se.Thread(other)
		return len(th.Undelivered()) == 0
	})
	a.Emit(agent.TurnStart{})
	test, _ := json.Marshal(map[string]string{"command": "go test ./..."})
	a.Emit(agent.ToolCall{ID: "c1", Tool: "Bash", Input: test})
	a.Emit(agent.ToolResult{ID: "c1", Output: "ok  \tpricing\t0.01s\n--- FAIL: TestSnapshot (0.00s)\nFAIL\tui\t0.01s\n", IsError: true})
	r.eventually(id, "the run", func(se core.SessionState) bool { return len(se.Runs) == 1 })
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Fixed: pricing's tests pass."})
	a.Call(t, "reply", map[string]any{"thread": string(other), "body": "Done too; the tests pass."})
	for _, th := range []core.ThreadID{core.ThreadID(tid), other} {
		t2, _ := r.session(id).Thread(th)
		if c, _ := t2.Last(); c.Unbacked != "" || len(c.Evidence) != 1 {
			t.Errorf("%s: evidence %+v, unbacked %q", th, c.Evidence, c.Unbacked)
		}
	}
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn's end", func(se core.SessionState) bool { return !se.Agent.Working })
	a.Emit(agent.TurnStart{})
	r.eventually(id, "the next turn", func(se core.SessionState) bool { return se.Agent.Working })
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Still: tests pass."})
	t3, _ := r.session(id).Thread(core.ThreadID(tid))
	if c, _ := t3.Last(); c.Unbacked == "" {
		t.Error("a claim in a turn with no runs of its own wasn't flagged")
	}
}

// A reply carries the model the agent ran as, by name with its version, once its tool calls have said it.
func TestReplyNamesTheModelItRanAs(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.ToolCall{ID: "c1", Tool: "Read", Model: "claude-opus-5-5", Input: json.RawMessage(`{}`)})
	r.eventually(id, "the call", func(se core.SessionState) bool {
		return slices.ContainsFunc(se.Transcript, func(it core.Item) bool { return it.ID == "c1" })
	})
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Done."})
	th, _ := r.session(id).Thread(core.ThreadID(tid))
	if c, _ := th.Last(); c.Model != "Opus 5.5 · high" {
		t.Errorf("the reply's model is %q", c.Model)
	}
	for id, want := range map[string]string{"claude-opus-5-5": "Opus 5.5", "claude-sonnet-4-5-20250929": "Sonnet 4.5", "claude-haiku-5": "Haiku 5", "gpt-5": ""} {
		if got := agentrun.ModelName(id); got != want {
			t.Errorf("ModelName(%q) = %q, want %q", id, got, want)
		}
	}
}

// Requests handed over together are the agent's, but only the one it says it's on is active: a working reply, or a
// task of it started. One handed over alone is active at once.
func TestOnlyTheRequestTheAgentIsOnIsActive(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("first")
	a.WaitSent(t)
	first := r.session(id).Threads[0].ID
	// The hand-over is in the state once its events are applied, a moment after the agent has it.
	r.eventually(id, "the request handed over alone busy and active", func(se core.SessionState) bool {
		th, _ := se.Thread(first)
		return th.Busy && th.Active
	})
	a.Emit(agent.TurnStart{})
	b, _ := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "second"})
	c, _ := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "third"})
	a.Call(t, "reply", map[string]any{"thread": string(first), "body": "Done.", "resolve": true})
	a.Emit(agent.TurnEnd{})
	a.WaitSent(t)
	r.eventually(id, "both handed over busy, neither active", func(se core.SessionState) bool {
		for _, tid := range []core.ThreadID{b, c} {
			if th, _ := se.Thread(tid); !th.Busy || th.Active {
				return false
			}
		}
		return true
	})
	a.Call(t, "reply", map[string]any{"thread": string(c), "body": "On it.", "working": true})
	out := a.Call(t, "task_add", map[string]any{"subject": "second", "thread": string(b)})
	a.Call(t, "task_update", map[string]any{"id": strings.TrimSuffix(strings.Fields(out)[1], ""), "status": "in_progress"})
	for _, tid := range []core.ThreadID{b, c} {
		if th, _ := r.session(id).Thread(tid); !th.Active {
			t.Errorf("%s the agent is on isn't active", tid)
		}
	}
}

// A call of Oriel's own reply says which request it answered and how it began, and opens on what it said: not
// "reply", then "replied".
func TestOrielToolCallsSayWhatTheyDid(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	tid := string(r.session(id).Threads[0].ID)
	in, _ := json.Marshal(map[string]any{"thread": tid, "body": "Fixed: `Floor` rounds toward -∞.\n\nmore", "resolve": true})
	a.Emit(agent.TurnStart{})
	a.Emit(agent.ToolCall{ID: "r1", Tool: "mcp__oriel__reply", Input: in})
	a.Emit(agent.ToolResult{ID: "r1", Output: "replied"})
	se := r.eventually(id, "the call done", func(se core.SessionState) bool {
		return slices.ContainsFunc(se.Transcript, func(it core.Item) bool { return it.ID == "r1" && it.Done })
	})
	it := se.Transcript[slices.IndexFunc(se.Transcript, func(it core.Item) bool { return it.ID == "r1" })]
	if want := "Reply and resolve on “Negative values round the wrong way”: Fixed: `Floor` rounds toward -∞."; it.Title != want {
		t.Errorf("title %q, want %q", it.Title, want)
	}
	if !strings.HasPrefix(it.Output, "Fixed: `Floor` rounds toward -∞.") || !strings.HasSuffix(it.Output, "→ replied") {
		t.Errorf("output %q", it.Output)
	}
}

// Neither an answer, reply-and-resolve nor resolve goes through on a thread holding a comment the agent hasn't been
// given: the error hands the comment over, and trying again works. A progress note still does.
func TestResolveRefusesUnseenComments(t *testing.T) {
	for _, tool := range []string{"reply", "answer", "resolve"} {
		t.Run(tool, func(t *testing.T) {
			r := newRig(t)
			id, a := r.workSession("Fix the rounding")
			a.WaitSent(t)
			tid := r.session(id).Threads[0].ID
			if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Thread: tid, Body: "and the zero case"}); err != nil {
				t.Fatal(err)
			}
			a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "On it.", "working": true})
			args := map[string]any{"thread": string(tid), "body": "Fixed.", "resolve": true}
			switch tool {
			case "resolve":
				args = map[string]any{"thread": string(tid)}
			case "answer":
				args = map[string]any{"thread": string(tid), "body": "Fixed."}
			}
			name := tool
			if tool == "answer" {
				name = "reply"
			}
			_, err := a.CallErr(name, args)
			if err == nil || !strings.Contains(err.Error(), "and the zero case") {
				t.Fatalf("resolving with an unseen comment: err = %v", err)
			}
			th, _ := r.session(id).Thread(tid)
			if !th.Open() || len(th.Undelivered()) != 0 {
				t.Fatalf("after the refusal: open %v, undelivered %v", th.Open(), th.Undelivered())
			}
			if slices.ContainsFunc(th.Comments, func(c core.Comment) bool { return c.Body == "Fixed." }) {
				t.Error("the refused reply was posted")
			}
			a.Call(t, name, args)
			th, _ = r.session(id).Thread(tid)
			if tool != "answer" && th.Open() {
				t.Error("resolving again, with nothing unseen, left the thread open")
			}
			if tool == "answer" && !slices.ContainsFunc(th.Comments, func(c core.Comment) bool { return c.Body == "Fixed." }) {
				t.Error("answering again, with nothing unseen, wasn't posted")
			}
		})
	}
}

// A shell command's changes to the session's projects are recorded on its item, as an edit's are.
func TestShellCommandsRecordWhatTheyChanged(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Write things")
	a.WaitSent(t)
	dir := r.session(id).Projects[0].Dir
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte("package gen\n\nfunc Gen() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "b1", Tool: "Bash", Input: json.RawMessage(`{"command":"python3 gen.py"}`)})
	a.Emit(agent.ToolResult{ID: "b1", Output: "ok"})
	se := r.eventually(id, "the command's files", func(se core.SessionState) bool {
		return slices.ContainsFunc(se.Transcript, func(it core.Item) bool { return it.ID == "b1" && len(it.Files) > 0 })
	})
	it := se.Transcript[slices.IndexFunc(se.Transcript, func(it core.Item) bool { return it.ID == "b1" })]
	if len(it.Files) != 1 || it.Files[0].Path != filepath.Join(dir, "gen.go") || it.Files[0].Added != 3 {
		t.Fatalf("files = %+v", it.Files)
	}
	if got := it.Files[0].Lines[2]; got.Op != "+" || got.New != 3 || got.Text != "func Gen() {}" {
		t.Errorf("line 3 = %+v", got)
	}
}

// gitRuns puts a git on PATH that logs each run before running the real one, and counts the runs so far.
func gitRuns(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "runs")
	shim := "#!/bin/sh\necho >> \"" + log + "\"\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(log)
		return strings.Count(string(b), "\n")
	}
}

// sevenProjects is a work session over seven repos, its first turn started and its first mark (of all seven) made,
// with the git runs that mark took. The app hands the host the session's freshness manager, as Oriel's does.
func sevenProjects(t *testing.T, runs func() int) (*rig, core.SessionID, *agenttest.FakeSession, int) {
	t.Helper()
	r := newRig(t)
	m := watch.New(watch.Options{Name: "the session"})
	t.Cleanup(m.Close)
	r.w.SetApp(App{Watches: func(core.SessionID) *watch.Manager { return m }})
	repos := []core.RepoID{core.RepoID(r.repo.Dir)}
	for i := range 6 {
		g := gittest.New(t)
		g.Write(map[string]string{fmt.Sprintf("p%d.go", i): "package p\n"})
		g.Commit("Ada", "first")
		repos = append(repos, core.RepoID(g.Dir))
	}
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Many projects", Provider: "fake", Model: "m1", Repos: repos})
	a := r.agent()
	a.WaitSent(t)
	before := runs()
	a.Emit(agent.TurnStart{})
	full := settled(r, a, runs) - before
	if full == 0 {
		t.Fatal("the turn's start made no mark")
	}
	return r, id, a, full
}

// settled is runs once a's events are handled and the marks they asked for are made.
func settled(r *rig, a *agenttest.FakeSession, runs func() int) int {
	r.t.Helper()
	a.Handled(r.t)
	r.settle()
	return runs()
}

// A command's mark snapshots only the projects the file watcher saw change, and still finds what the command did:
// one that changed nothing runs no git, and one that wrote a file in one project snapshots that one and records it.
func TestShellMarksOnlyWhatChanged(t *testing.T) {
	runs := gitRuns(t)
	r, id, a, full := sevenProjects(t, runs)
	before := runs()
	a.Emit(agent.ToolCall{ID: "quiet", Tool: "Bash", Input: json.RawMessage(`{"command":"true"}`)})
	a.Emit(agent.ToolResult{ID: "quiet", Output: "ok"})
	if n := settled(r, a, runs) - before; n != 0 {
		t.Errorf("a command that changed nothing ran git %d times", n)
	}
	dir := r.session(id).Projects[3].Dir
	before = runs()
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte("package gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "gen", Tool: "Bash", Input: json.RawMessage(`{"command":"python3 gen.py"}`)})
	a.Emit(agent.ToolResult{ID: "gen", Output: "ok"})
	se := r.eventually(id, "the command's files", func(se core.SessionState) bool {
		return slices.ContainsFunc(se.Transcript, func(it core.Item) bool { return it.ID == "gen" && len(it.Files) > 0 })
	})
	it := se.Transcript[slices.IndexFunc(se.Transcript, func(it core.Item) bool { return it.ID == "gen" })]
	if len(it.Files) != 1 || it.Files[0].Path != filepath.Join(dir, "gen.go") {
		t.Errorf("files = %+v", it.Files)
	}
	// One project's snapshot and its diff: well under the seven projects' snapshots.
	if n := settled(r, a, runs) - before; n > full/3 {
		t.Errorf("a change to one project ran git %d times; marking all seven took %d", n, full)
	}
}
