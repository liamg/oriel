package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
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
func planSession(t *testing.T) (*running, *agenttest.FakeSession, *localHost) {
	t.Helper()
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "plan", "name": "rounding", "provider": "fake",
			"prompt": "Round invoice lines half-to-even", "repos": []string{lw.repo}})
	})
	if err != nil {
		t.Fatal(err)
	}
	a := lw.fake.WaitStarted(t)
	if in := a.WaitSent(t); !strings.Contains(in.Text, `where="plan"`) {
		t.Fatalf("the prompt reached the agent as %q", in.Text)
	}
	waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	return r, a, lw
}

func planNow(t *testing.T, r *running) core.SessionState {
	t.Helper()
	se, ok := r.store.Session("rounding")
	if !ok {
		t.Fatal("no session rounding")
	}
	return se.Snapshot()
}

// AI-8, done when: "the crit plan flow works end to end". ui.md 05: "Plan docs render as documents, with threads in
// the margin anchored to lines", and "Readiness: Start work unlocks when every thread is resolved or deferred";
// sessions.md: "Start work makes the worktrees, on the session's branch". The agent drafts PLAN.md, you comment on a
// line of it, the agent answers, you defer the thread, and Start work makes the worktree and sets the agent to work.
func TestPlanFlowWorksEndToEnd(t *testing.T) {
	r, a, lw := planSession(t)
	r.waitFrame("the plan view, waiting for its docs", func(s string) bool { return containsAll(s, "PLAN DOCS", "READINESS") })

	// The agent drafts the plan and says it's ready.
	se := planNow(t, r)
	doc := filepath.Join(se.Dir, "PLAN.md")
	a.Emit(agent.TurnStart{})
	if err := os.WriteFile(doc, []byte(planMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.ToolCall{ID: "w1", Tool: "Write", Input: toolInput(map[string]string{"file_path": doc})})
	a.Emit(agent.ToolResult{ID: "w1", Diff: &core.FileDiff{Path: doc, Added: 11}})
	a.Call(t, "reply", map[string]any{"thread": string(se.PlanThread()), "body": "The plan rounds each line half-to-even: ready to annotate.", "resolve": true})
	a.Emit(agent.TurnEnd{})
	r.waitFrame("PLAN.md rendered", func(s string) bool {
		return containsAll(s, "PLAN.md", "Round invoice lines half-to-even", "Approach", "Round each line in minor units, ties to even.", "•") &&
			!strings.Contains(s, "## Approach")
	})
	r.waitText("Ready: every")

	// The keys move through the doc's blocks and open a thread on one; Escape drops it.
	r.typeKeys("jjjjjc")
	r.waitFrame("a thread being opened on the fifth block", func(s string) bool { return containsAll(s, "New thread", "· L9") })
	r.typeKeys("<Esc>")
	r.waitGone("New thread")

	// A comment on a line of it reaches the agent anchored to that line.
	r.clickText("Round each line in minor units, ties to even.")
	r.waitText("New thread")
	r.typeText("Is half-even what finance wants?")
	r.waitText("Is half-even what finance wants?")
	r.typeKeys("<CR>")
	in := a.WaitSent(t)
	for _, want := range []string{`where="PLAN.md:10"`, "<anchor>- Round each line in minor units, ties to even.</anchor>", "Is half-even what finance wants?"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the comment reached the agent without %q:\n%s", want, in.Text)
		}
	}
	r.waitText("1 open")
	r.waitText("Start work unlocks when")
	r.clickText("Start work")
	time.Sleep(200 * time.Millisecond)
	if !planNow(t, r).Planning() {
		t.Fatal("Start work ran with a thread open")
	}

	// The agent answers in the margin; deferring the thread readies the plan.
	var tid core.ThreadID
	for _, th := range planNow(t, r).Threads {
		if th.Anchor != nil {
			tid = th.ID
		}
	}
	a.Emit(agent.TurnStart{})
	a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Stripe reconciles half-even; half-up keeps the drift."})
	a.Emit(agent.TurnEnd{})
	r.waitFrame("the answer in the margin", func(s string) bool {
		return containsAll(s, "Stripe reconciles half-even;", "NEEDS YOU")
	})
	r.clickText("defer")
	r.waitText("Ready: every")

	// Start work: the dialog, then the worktree, a fresh agent told where, and the window on the worktree.
	r.clickText("Start work")
	r.waitText("START WORK")
	r.typeKeys("<D-CR>")
	fresh := lw.fake.WaitStarted(t)
	in = fresh.WaitSent(t)
	st := waitState(t, r.store, "work started", func(st core.State) bool {
		s, _ := st.Session("rounding")
		return s != nil && s.Snapshot().Started
	})
	s, _ := st.Session("rounding")
	p, _ := s.Snapshot().Primary()
	if p.ReadOnly || p.Dir != filepath.Join(se.Dir, "repos", filepath.Base(lw.repo)) {
		t.Fatalf("the project after Start work: %+v", p)
	}
	if !strings.Contains(in.Text, "Start work") || !strings.Contains(in.Text, p.Dir) {
		t.Errorf("the agent was told %q", in.Text)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var dir string
		r.do(func() error { dir = r.host.Here().Project; return nil })
		if filepath.Clean(dir) == filepath.Clean(p.Dir) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window stayed on %s, not the worktree %s", dir, p.Dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mdParse keeps each block's source lines, so a thread on a line finds its block: a paragraph runs to its last line,
// a list item takes its continuation lines, and a fence its body.
func TestMarkdownBlocksKeepTheirLines(t *testing.T) {
	src := "# Title\n\nFirst line\nsecond line.\n\n- one\n  more\n- two\n\n```mermaid\nflowchart LR\n```\n---\n"
	want := []mdBlock{
		{Kind: mdHeading, Level: 1, Text: "Title", Line: 1, End: 1},
		{Kind: mdPara, Text: "First line second line.", Line: 3, End: 4},
		{Kind: mdItem, Mark: "•", Text: "one more", Line: 6, End: 7},
		{Kind: mdItem, Mark: "•", Text: "two", Line: 8, End: 8},
		{Kind: mdCode, Lang: "mermaid", Code: []string{"flowchart LR"}, Line: 10, End: 12},
		{Kind: mdRule, Line: 13, End: 13},
	}
	got := mdParse(src)
	if len(got) != len(want) {
		t.Fatalf("%d blocks: %+v", len(got), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.Level != w.Level || g.Mark != w.Mark || g.Lang != w.Lang || strings.Join(g.Code, "\n") != strings.Join(w.Code, "\n") ||
			g.Line != w.Line || g.End != w.End {
			t.Errorf("block %d = %+v, want %+v", i, g, w)
		}
	}
}

// clickAccess clicks the control named label, found in the window's accessibility tree where it's settled: in the
// same place two frames running.
func (r *running) clickAccess(label string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *render.AccessNode
	for {
		var found *render.AccessNode
		var find func(n *render.AccessNode)
		find = func(n *render.AccessNode) {
			if n.Name == label {
				found = n
			}
			for _, k := range n.Children {
				find(k)
			}
		}
		if tr := r.win.Access(); tr != nil {
			find(tr)
		}
		settled := found != nil && last != nil && found.Bounds == last.Bounds
		last = found
		if settled {
			at := render.Point{X: (found.Bounds.Min.X + found.Bounds.Max.X) / 2, Y: (found.Bounds.Min.Y + found.Bounds.Max.Y) / 2}
			r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
			r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonPrimary})
			r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonPrimary})
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("nothing named %q to click", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sessions.md, Plan → Start work: Start work opens a dialog over the plan, which Escape, its esc and Cancel close.
// ⌘↵ starts work as it says: here with the plan's agent kept (its toggle clicked off), a branch renamed, and the
// plan's threads cleared, the deferred one carried into what the agent is told.
func TestStartWorkDialog(t *testing.T) {
	r, a, lw := planSession(t)
	se := planNow(t, r)
	if err := os.WriteFile(filepath.Join(se.Dir, "PLAN.md"), []byte(planMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, th := range []struct {
		body   string
		status core.ThreadStatus
	}{{"Is half-even what finance wants?", core.ThreadDeferred}, {"Which currencies?", core.ThreadResolved}} {
		tid, err := lw.w.Comment(ctx, host.CommentReq{Session: "rounding", Body: th.body,
			Anchor: &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: 10}})
		if err != nil {
			t.Fatal(err)
		}
		if err := lw.w.SetThread(host.ThreadReq{Session: "rounding", Thread: tid, Status: th.status}); err != nil {
			t.Fatal(err)
		}
	}
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.waitText("Ready: every")

	for _, close := range []func(){
		func() { r.typeKeys("<Esc>") },
		func() { r.clickTop("esc") },
		func() { r.clickTop("Cancel") },
	} {
		r.clickText("Start work")
		r.waitFrame("the Start work dialog", func(s string) bool {
			return containsAll(s, "START WORK", "Clear the plan's threads", "Restart the agent with a fresh context", "1 goes · 1 carried")
		})
		close()
		r.waitGone("START WORK")
	}
	if !planNow(t, r).Planning() {
		t.Fatal("closing the dialog started work")
	}

	r.clickText("Start work")
	r.waitText("START WORK")
	r.clickAccess("Restart the agent with a fresh context")
	r.waitText("keeps the plan's conversation")
	branch := planNow(t, r).Projects[0].Branch
	r.clickAccess("Branch for pricing")
	r.typeKeys(strings.Repeat("<BS>", len(branch)))
	r.typeText("tester/rounding-half-even")
	r.waitText("tester/rounding-half-even")
	r.typeKeys("<D-CR>")
	r.waitGone("START WORK")
	in := a.WaitSent(t)
	for _, want := range []string{"Start work", "(deferred): Is half-even what finance wants?"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the plan's agent was told, without %q:\n%s", want, in.Text)
		}
	}
	if strings.Contains(in.Text, "Which currencies?") {
		t.Errorf("a resolved thread was carried:\n%s", in.Text)
	}
	se = planNow(t, r)
	if p, _ := se.Primary(); !se.Started || p.Branch != "tester/rounding-half-even" || len(se.Threads) != 1 || a.Closed() {
		t.Errorf("after Start work: started %v, branch %q, %d threads, agent closed %v", se.Started, p.Branch, len(se.Threads), a.Closed())
	}
}

// session.start-work from Lua or the CLI, without the dialog: {clear} and {fresh} are on unless false, {branches} is
// a table by project, and what's absent is the session's own.
func TestStartWorkArgs(t *testing.T) {
	r := startWorkReq("s", registry.Args{"clear": false, "branches": map[string]any{"pricing": "liam/x"}, "sandbox": true, "network": "open",
		"model": "opus"})
	if !r.KeepThreads || r.KeepConversation || r.Branches["pricing"] != "liam/x" || r.Sandbox == nil || !*r.Sandbox || r.Network != "open" ||
		r.Model != "opus" || r.Effort != "" {
		t.Errorf("%+v", r)
	}
	if r := startWorkReq("s", nil); r.KeepThreads || r.KeepConversation || r.Sandbox != nil || r.Branches != nil {
		t.Errorf("no args: %+v", r)
	}
}
