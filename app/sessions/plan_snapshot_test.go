package sessions_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
)

// mockupPlan is the scene's PLAN.md, its lines where the threads are: L14, L22 and L31.
const mockupPlan = "# Round invoice lines half-to-even\n\n## Goal\n\n" +
	"Invoice totals must equal the sum of their rounded lines. Today they drift by up to one minor unit on tiered plans.\n\n" +
	"## Approach\n\n\n\n\n\n" +
	"- Add a `RoundingMode` type in `internal/pricing`.\n" +
	"- Round each line in minor units, ties to even.\n" +
	"- Totals sum the rounded lines; never re-round a total.\n\n\n\n\n\n\n" +
	"Currency precision comes from `currency.go`, keyed by ISO 4217 code, so JPY (0) and BHD (3) are handled.\n\n" +
	"## Pieces\n" +
	"```mermaid\nflowchart LR\n  usage.Meter --> pricing.Round --> billing.Invoice\n```\n" +
	"## Risks\n\n" +
	"- Historical invoices must not change: only new invoices round this way.\n" +
	"- Partners reconcile against our CSV export; flag the change in the changelog.\n"

// planScene is a session, pricing-rounding, planning: three docs, Claude drafting rev 4 of PLAN.md and just
// edited its precision line, a thread on L14 that needs you, one on L22 resolved, one on L31 Claude is working on,
// and one on api.md.
func planScene(t *testing.T) *apptest.Scene {
	t.Helper()
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "PLAN.md"), []byte(mockupPlan), 0o644)
	os.WriteFile(filepath.Join(ws, "api.md"), []byte("# API\n\n- `pricing.Round(amount, mode)`\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "ui.md"), []byte("# UI\n\nNothing changes on screen.\n"), 0o644)
	pricing, billing := filepath.Join(t.TempDir(), "pricing"), filepath.Join(t.TempDir(), "billing-ui")
	os.MkdirAll(pricing, 0o755)
	os.MkdirAll(billing, 0o755)
	s := apptest.New(t, apptest.Options{WorkDir: pricing})
	s.Now = time.Date(2026, 10, 13, 14, 30, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return s.Now.Add(-d) }
	liam := "Liam Galvin"
	doc := func(file string, line int, text string) *core.Anchor {
		return &core.Anchor{Project: core.DocsProject, File: file, Line: line, Text: text}
	}
	plan := filepath.Join(ws, "PLAN.md")
	edit := core.FileDiff{Path: plan, Added: 1, Removed: 1, Lines: []core.DiffLine{
		{Op: "-", Old: 22, Text: "Currency precision comes from a hard-coded table in `round.go`, keyed by ISO 4217 code, so JPY (0) and BHD (3) are handled."},
		{Op: "+", New: 22, Text: "Currency precision comes from `currency.go`, keyed by ISO 4217 code, so JPY (0) and BHD (3) are handled."},
	}}
	var transcript []core.Item
	for i := range 4 {
		d := edit
		transcript = append(transcript, core.Item{ID: string(rune('a' + i)), Kind: core.ItemTool, Tool: "Edit", Diff: &d, Time: ago(time.Duration(4-i) * time.Minute)})
	}
	transcript[3].Time = ago(4 * time.Second)
	se := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindPlan, StartWork: true, Status: core.StatusNeedsYou, Dir: ws,
		Ticket: "ENG-2231", Prompt: "Rounding on invoices drifts by 1¢ for tiered usage plans.",
		Projects: []core.Project{{Name: "pricing", Repo: pricing, Dir: pricing, Branch: "rethink/rounding", Primary: true, ReadOnly: true},
			{Name: "billing-ui", Repo: billing, Dir: billing, Branch: "rethink/rounding", ReadOnly: true}},
		Agent: core.AgentState{Provider: "claude", Model: "opus", Running: true, Working: true, TurnStarted: ago(30 * time.Second),
			Stats: core.Stats{Context: 36_000, ContextMax: 200_000, Cost: 0.62}},
		Transcript: transcript,
		Threads: []core.Thread{
			{ID: "0b1d2e3f", Status: core.ThreadResolved, Created: ago(20 * time.Minute), Updated: ago(15 * time.Minute),
				Comments: []core.Comment{{ID: "c0", Author: core.AuthorYou, Name: liam, Body: "Rounding on invoices drifts by 1¢ for tiered usage plans."}}},
			{ID: "a91c4d2e", Status: core.ThreadOpen, Created: ago(6 * time.Minute), Updated: ago(5 * time.Minute),
				Anchor: doc("PLAN.md", 14, "- Round each line in minor units, ties to even."),
				Comments: []core.Comment{
					{ID: "c1", Author: core.AuthorYou, Name: liam, Delivered: true, Time: ago(6 * time.Minute), Body: "Is half-even what finance actually wants? I'd assumed half-up."},
					{ID: "c2", Author: core.AuthorAgent, Name: "claude", Time: ago(5 * time.Minute),
						Body: "The Stripe invoices you reconcile against use half-even (`docs/billing.md:40`). Half-up would keep the 1¢ drift. Confirm and I'll note it under Risks?"},
				}},
			{ID: "5e6f7a8b", Status: core.ThreadResolved, Created: ago(12 * time.Minute), Updated: ago(4 * time.Second),
				Anchor: doc("PLAN.md", 22, "Currency precision comes from a hard-coded table in `round.go`, keyed by ISO 4217 code, so JPY (0) and BHD (3) are handled."),
				Comments: []core.Comment{
					{ID: "c3", Author: core.AuthorYou, Name: liam, Delivered: true, Time: ago(12 * time.Minute), Body: "Where does precision come from?"},
					{ID: "c4", Author: core.AuthorAgent, Name: "claude", Time: ago(4 * time.Second), Body: "Moved it to `currency.go`: edited."},
				}},
			{ID: "3c4d5e6f", Status: core.ThreadOpen, Busy: true, Created: ago(time.Minute), Updated: ago(time.Second),
				Anchor: doc("PLAN.md", 31, "- Historical invoices must not change: only new invoices round this way."),
				Comments: []core.Comment{
					{ID: "c5", Author: core.AuthorYou, Name: liam, Delivered: true, Time: ago(time.Minute), Body: "How do we guarantee this? A flag on the invoice?"},
					{ID: "c6", Author: core.AuthorAgent, Name: "claude", Progress: true, Time: ago(time.Second), Body: "reading billing/invoice.go… checking for a `version` column"},
				}},
			{ID: "7d8e9f0a", Status: core.ThreadOpen, Created: ago(3 * time.Minute), Updated: ago(3 * time.Minute),
				Anchor:   doc("api.md", 3, "- `pricing.Round(amount, mode)`"),
				Comments: []core.Comment{{ID: "c7", Author: core.AuthorYou, Name: liam, Delivered: true, Time: ago(3 * time.Minute), Body: "Should mode be optional?"}}},
		},
	}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "Claude Max", Windows: []core.UsageWindow{
			{Name: "5h window", Used: .38}, {Name: "weekly", Used: .71}}}})
	return s
}

// PLAN.md rendered as a document with its line numbers where threads are, the agent's latest edit
// as an inline diff ("agent edited 4s ago"), the threads in the margin beside their lines (needs you, resolved
// folded, Claude working), the docs with their open threads, the source ticket, the projects, and readiness.
func TestPlanSnapshot(t *testing.T) {
	s := planScene(t)
	s.Run("plan.open", nil)
	s.Frame()
	s.Frame()
	s.Frame()
	s.Lint("the plan view")
	s.Golden("plan")
	text := s.Text()
	for _, want := range []string{"PLAN DOCS", "3 files", "PLAN.md", "api.md", "ui.md", "SOURCE", "ENG-2231", "PROJECTS", "pricing", "billing-ui",
		"rethink/rounding", "READINESS", "3 open", "Start work unlocks when every thread",
		"PLAN", "Round invoice lines half-to-even", "claude opus · drafting rev 4", "agent editing PLAN.md", "Preview diff", "Start work",
		"PLAN.MD · REV 4", "Goal · Approach · Pieces · Risks", "Round each line in minor units, ties to even.", "AGENT EDITED 4S AGO",
		"a hard-coded table in", "MERMAID · FLOWCHART", "usage.Meter", "pricing.Round", "billing.Invoice", "14", "31", "· L14 ·", "NEEDS YOU", "Is half-even what finance",
		"L22", "RESOLVED", "CLAUDE · WORKING", "How do we guarantee this?"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
	if strings.Contains(text, "Should mode be optional?") {
		t.Error("api.md's thread shows in PLAN.md's margin")
	}
}

// The margin's threads sit beside the lines they're about: each card's top is level with its block's.
func TestPlanThreadsSitBesideTheirLines(t *testing.T) {
	s := planScene(t)
	s.Run("plan.open", nil)
	for range 4 {
		s.Frame()
	}
	for _, pair := range [][2]string{{"Round each line", "Is half-even"}, {"Historical invoices", "How do we guarantee"}} {
		line, ok1 := s.TextStart(pair[0])
		card, ok2 := s.TextStart(pair[1])
		if !ok1 || !ok2 {
			t.Fatalf("missing %q or %q", pair[0], pair[1])
		}
		// The card's comment is under its header, a little below the line it's about; never above it.
		if d := card.Y - line.Y; d < 0 || d > 120 {
			t.Errorf("%q is %vpx from its line", pair[1], d)
		}
	}
}

// Plan → Start work: the dialog over the plan, with the plan's threads to clear, a fresh agent on the
// plan's model, a branch per project, and the session's sandbox and network.
func TestStartWorkDialogSnapshot(t *testing.T) {
	s := planScene(t)
	se, _ := s.Store().Session("pricing-rounding")
	for _, th := range se.Snapshot().Threads {
		if th.Status == core.ThreadOpen && th.Anchor != nil {
			th.Status = core.ThreadDeferred
			s.Store().Apply(core.ThreadUpdated{Session: "pricing-rounding", Thread: th})
		}
	}
	s.Run("plan.open", nil)
	s.Run("plan.start-work", nil)
	for range 3 {
		s.Frame()
	}
	s.Lint("the Start work dialog")
	s.Golden("plan-start-work")
	text := s.Text()
	for _, want := range []string{"START WORK", "pricing-rounding", "THREADS", "1 goes · 3 carried", "Clear the plan's threads",
		"Restart the agent with a fresh context", "from the plan docs and the work pack", "opus", "BRANCHES", "pricing", "billing-ui",
		"rethink/rounding", "RUN ON", "Sandbox", "Open network", "Cancel", "Start work"} {
		if !strings.Contains(text, want) {
			t.Errorf("the dialog lacks %q", want)
		}
	}
}

// A closed thread in the margin unfolds to its card for reading and collapses back, and stays resolved throughout.
func TestPlanClosedThreadUnfoldsWithoutReopening(t *testing.T) {
	s := planScene(t)
	s.Run("plan.open", nil)
	s.Until("the plan", func() bool { return s.Has("PLAN.MD · REV 4") })
	s.Frames(3)
	if s.Has("Where does precision come from?") {
		t.Fatal("the resolved thread's comment shows before it's unfolded")
	}
	replies := s.Count("Reply to the thread")
	s.ClickNode("Show the thread")
	if !s.Has("Where does precision come from?") {
		t.Fatal("the resolved thread didn't unfold")
	}
	s.Node("Collapse the thread")
	s.Node("reopen")
	if s.Count("Reply to the thread") != replies {
		t.Error("an unfolded closed thread offers a reply")
	}
	se, _ := s.Store().Session("pricing-rounding")
	if th, _ := se.Snapshot().Thread("5e6f7a8b"); th.Status != core.ThreadResolved {
		t.Errorf("unfolding made the thread %s", th.Status)
	}
	s.ClickNode("Collapse the thread")
	if s.Has("Where does precision come from?") {
		t.Error("the thread didn't fold back")
	}
}
