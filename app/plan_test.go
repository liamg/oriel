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
	"github.com/liamg/oriel/registry"
)

const planMarkdown = `# Round invoice lines half-to-even

## Goal

Invoice totals must equal the sum of their rounded lines.

## Approach

- Add a ` + "`RoundingMode`" + ` type in ` + "`internal/pricing`" + `.
- Round each line in minor units, ties to even.
- Totals sum the rounded lines; never re-round a total.
`

// planSession starts a plan session with the fake agent on the worker's repo and waits for its prompt to reach the
// agent and the window to follow it.
func planSession(t *testing.T) (*running, *agenttest.FakeSession, *localWorker) {
	t.Helper()
	lw := newLocalWorker(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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

	// Start work: the worktree, the agent told where, and the window on the worktree.
	r.clickText("Start work")
	in = a.WaitSent(t)
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
