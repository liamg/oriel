package sessions_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

const planMarkdown = `# Round invoice lines half-to-even

## Goal

Invoice totals must equal the sum of their rounded lines.

## Approach

- Add a ` + "`RoundingMode`" + ` type in ` + "`internal/pricing`" + `.
- Round each line in minor units, ties to even.
- Totals sum the rounded lines; never re-round a total.
`

// planSession starts a plan session with the fake agent on the host's repo and waits for its prompt to reach the
// agent and the window to follow it.
func planSession(t *testing.T) (*apptest.Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	w, lw := apptest.HostWindow(t)
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	w.Run("session.start", registry.Args{"kind": "plan", "name": "rounding", "provider": "fake", "prompt": "Round invoice lines half-to-even",
		"repos": []string{lw.Repo}})
	a := lw.Fake.WaitStarted(t)
	if in := a.WaitSent(t); !strings.Contains(in.Text, `where="plan"`) {
		t.Fatalf("the prompt reached the agent as %q", in.Text)
	}
	w.UntilState("the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	return w, a, lw
}

func planNow(t *testing.T, w *apptest.Window) core.SessionState {
	t.Helper()
	se, ok := w.Store.Session("rounding")
	if !ok {
		t.Fatal("no session rounding")
	}
	return se.Snapshot()
}

// The plan flow end to end. Plan docs render as documents, with threads in the margin anchored to lines; Start work
// unlocks when every thread is resolved or deferred, and makes the worktrees on the session's branch. The agent
// drafts PLAN.md, you comment on a line of it, the agent answers, you defer the thread, and Start work makes the
// worktree and sets the agent to work.
func TestPlanFlowWorksEndToEnd(t *testing.T) {
	w, a, lw := planSession(t)
	w.WaitFrame("the plan view, waiting for its docs", func(s string) bool { return apptest.HasAll(s, "PLAN DOCS", "READINESS") })

	// The agent drafts the plan and says it's ready.
	se := planNow(t, w)
	doc := filepath.Join(se.Dir, "PLAN.md")
	a.Emit(agent.TurnStart{})
	if err := os.WriteFile(doc, []byte(planMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "w1", Tool: "Write", Input: rig.ToolInput(map[string]string{"file_path": doc})})
	a.Emit(agent.ToolResult{ID: "w1", Diff: &core.FileDiff{Path: doc, Added: 11}})
	a.Call(t, "reply", map[string]any{"thread": string(se.PlanThread()), "body": "The plan rounds each line half-to-even: ready to annotate.", "resolve": true})
	a.Emit(agent.TurnEnd{})
	w.WaitFrame("PLAN.md rendered", func(s string) bool {
		return apptest.HasAll(s, "PLAN.md", "Round invoice lines half-to-even", "Approach", "Round each line in minor units, ties to even.", "•") &&
			!strings.Contains(s, "## Approach")
	})
	w.WaitText("Ready: every")

	// The keys move through the doc's blocks and open a thread on one; Escape drops it.
	w.TypeKeys("jjjjjc")
	w.WaitFrame("a thread being opened on the fifth block", func(s string) bool { return apptest.HasAll(s, "New thread", "· L9") })
	w.TypeKeys("<Esc>")
	w.WaitGone("New thread")

	// A comment on a line of it reaches the agent anchored to that line.
	w.ClickText("Round each line in minor units, ties to even.")
	w.WaitText("New thread")
	w.TypeText("Is half-even what finance wants?")
	w.WaitText("Is half-even what finance wants?")
	w.TypeKeys("<CR>")
	in := a.WaitSent(t)
	for _, want := range []string{`where="PLAN.md:10"`, "<anchor>- Round each line in minor units, ties to even.</anchor>", "Is half-even what finance wants?"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the comment reached the agent without %q:\n%s", want, in.Text)
		}
	}
	w.WaitText("1 open")
	w.WaitText("Start work unlocks when")
	w.WaitAccess("Start work disabled with a thread open", func(w *apptest.Window) bool {
		n := w.Access("Start work")
		return n != nil && n.Disabled
	})
	w.Click(apptest.Centre(w.Access("Start work").Bounds)) // clicked anyway, as it is
	// The click handled, and whatever it set going done.
	w.Idle()
	if fs := w.Win.Frames(); strings.Contains(fs[len(fs)-1].Text(), "START WORK") || !planNow(t, w).Planning() {
		t.Fatal("Start work ran with a thread open")
	}

	// The agent answers in the margin; deferring the thread readies the plan.
	var tid core.ThreadID
	for _, th := range planNow(t, w).Threads {
		if th.Anchor != nil {
			tid = th.ID
		}
	}
	a.Emit(agent.TurnStart{})
	a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Stripe reconciles half-even; half-up keeps the drift."})
	a.Emit(agent.TurnEnd{})
	w.WaitFrame("the answer in the margin", func(s string) bool {
		return apptest.HasAll(s, "Stripe reconciles half-even;", "NEEDS YOU")
	})
	w.ClickText("defer")
	// Ready now, not as it was before the comment: WaitText finds any frame since the window opened.
	w.WaitAccess("Start work enabled", func(w *apptest.Window) bool {
		n := w.Access("Start work")
		return n != nil && !n.Disabled
	})
	w.Latest("ready", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Ready: every") })

	// Start work: the dialog, then the worktree, a fresh agent told where, and the window on the worktree.
	w.ClickText("Start work")
	w.WaitText("START WORK")
	w.TypeKeys("<D-CR>")
	fresh := lw.Fake.WaitStarted(t)
	in = fresh.WaitSent(t)
	st := w.UntilState("work started", func(st core.State) bool {
		s, _ := st.Session("rounding")
		return s != nil && s.Snapshot().Started
	})
	s, _ := st.Session("rounding")
	p, _ := s.Snapshot().Primary()
	if p.ReadOnly || p.Dir != filepath.Join(se.Dir, "repos", filepath.Base(lw.Repo)) {
		t.Fatalf("the project after Start work: %+v", p)
	}
	if !strings.Contains(in.Text, "Start work") || !strings.Contains(in.Text, p.Dir) {
		t.Errorf("the agent was told %q", in.Text)
	}
	w.WaitFor("the window on the worktree "+p.Dir, func() bool { return filepath.Clean(w.Host.Here().Project) == filepath.Clean(p.Dir) })
}

// Plan → Start work: Start work opens a dialog over the plan, which Escape, its esc and Cancel close.
// ⌘↵ starts work as it says: here with the plan's agent kept (its toggle clicked off), a branch renamed, and the
// plan's threads cleared, the deferred one carried into what the agent is told.
func TestStartWorkDialog(t *testing.T) {
	w, a, lw := planSession(t)
	se := planNow(t, w)
	if err := os.WriteFile(filepath.Join(se.Dir, "PLAN.md"), []byte(planMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, th := range []struct {
		body   string
		status core.ThreadStatus
	}{{"Is half-even what finance wants?", core.ThreadDeferred}, {"Which currencies?", core.ThreadResolved}} {
		tid, err := lw.W.Comment(ctx, host.CommentReq{Session: "rounding", Body: th.body,
			Anchor: &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: 10}})
		if err != nil {
			t.Fatal(err)
		}
		if err := lw.W.SetThread(host.ThreadReq{Session: "rounding", Thread: tid, Status: th.status}); err != nil {
			t.Fatal(err)
		}
	}
	a.Emit(agent.TurnStart{})
	rig.Answered(t, w.Store, a)
	a.Emit(agent.TurnEnd{})
	w.WaitText("Ready: every")

	for _, close := range []func(){
		func() { w.TypeKeys("<Esc>") },
		func() { w.ClickTop("esc") },
		func() { w.ClickTop("Cancel") },
	} {
		w.ClickText("Start work")
		w.WaitFrame("the Start work dialog", func(s string) bool {
			return apptest.HasAll(s, "START WORK", "Clear the plan's threads", "Restart the agent with a fresh context", "1 goes · 1 carried")
		})
		close()
		w.WaitGone("START WORK")
	}
	if !planNow(t, w).Planning() {
		t.Fatal("closing the dialog started work")
	}

	w.ClickText("Start work")
	w.WaitText("START WORK")
	w.ClickAccess("Restart the agent with a fresh context")
	w.WaitText("keeps the plan's conversation")
	branch := planNow(t, w).Projects[0].Branch
	w.ClickAccess("Branch for pricing")
	w.TypeKeys(strings.Repeat("<BS>", len(branch)))
	w.TypeText("tester/rounding-half-even")
	w.WaitText("tester/rounding-half-even")
	w.TypeKeys("<D-CR>")
	w.WaitGone("START WORK")
	in := a.WaitSent(t)
	for _, want := range []string{"Start work", "(deferred): Is half-even what finance wants?"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the plan's agent was told, without %q:\n%s", want, in.Text)
		}
	}
	if strings.Contains(in.Text, "Which currencies?") {
		t.Errorf("a resolved thread was carried:\n%s", in.Text)
	}
	se = planNow(t, w)
	if p, _ := se.Primary(); !se.Started || p.Branch != "tester/rounding-half-even" || len(se.Threads) != 1 || a.Closed() {
		t.Errorf("after Start work: started %v, branch %q, %d threads, agent closed %v", se.Started, p.Branch, len(se.Threads), a.Closed())
	}
}

// Start work's model menus are optional: while they're read the dialog says what Start uses meanwhile, and Start
// still goes.
func TestStartWorkModelsOptional(t *testing.T) {
	w, a, lw := planSession(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	w.WaitText("Ready: every")
	lw.Fake.Hold = make(chan struct{})
	w.ClickText("Start work")
	w.WaitText("Reading models…")
	w.WaitText("Will use the session's model if you go on now")
	close(lw.Fake.Hold)
	w.WaitGone("Reading models…")
	w.TypeKeys("<D-CR>")
	w.WaitGone("START WORK")
}

// A line whose thread the agent resolved takes a new thread: clicking it again opens the composer.
func TestAResolvedLineTakesANewThreadInTheWindow(t *testing.T) {
	w, a, _ := planSession(t)
	w.WaitFrame("the plan view, waiting for its docs", func(s string) bool { return apptest.HasAll(s, "PLAN DOCS", "READINESS") })
	se := planNow(t, w)
	doc := filepath.Join(se.Dir, "PLAN.md")
	a.Emit(agent.TurnStart{})
	if err := os.WriteFile(doc, []byte(planMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "w1", Tool: "Write", Input: rig.ToolInput(map[string]string{"file_path": doc})})
	a.Emit(agent.ToolResult{ID: "w1", Diff: &core.FileDiff{Path: doc, Added: 11}})
	a.Call(t, "reply", map[string]any{"thread": string(se.PlanThread()), "body": "Ready to annotate.", "resolve": true})
	a.Emit(agent.TurnEnd{})
	w.WaitText("Round each line in minor units, ties to even.")

	w.ClickText("Round each line in minor units, ties to even.")
	w.WaitText("New thread")
	w.TypeText("Is half-even what finance wants?")
	w.WaitText("Is half-even what finance wants?")
	w.TypeKeys("<CR>")
	a.WaitSent(t)
	w.WaitGone("New thread")
	var tid core.ThreadID
	for _, th := range planNow(t, w).Threads {
		if th.Anchor != nil {
			tid = th.ID
		}
	}
	a.Emit(agent.TurnStart{})
	a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Yes: Stripe reconciles half-even.", "resolve": true})
	a.Emit(agent.TurnEnd{})
	w.Latest("the thread resolved", func(f headless.Frame) bool {
		return strings.Contains(f.Text(), "RESOLVED") && !strings.Contains(f.Text(), "New thread")
	})

	w.ClickText("Round each line in minor units, ties to even.")
	w.Latest("a new thread on the line", func(f headless.Frame) bool { return strings.Contains(f.Text(), "New thread") })
}
