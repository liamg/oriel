package rig

import (
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// RoundGo is the fixture 01-ide shows: internal/pricing/round.go, with lines 38 to 51 as the mockup has them.
func RoundGo() string {
	var b strings.Builder
	b.WriteString("package pricing\n\nimport (\n\t\"github.com/shopspring/decimal\"\n)\n\n")
	for i := 7; i < 38; i++ {
		fmt.Fprintf(&b, "var tier%d = decimal.NewFromInt(%d)\n", i, i)
	}
	for _, l := range []string{
		"// roundHalfEven rounds v to p decimal places, ties to even.",
		"func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {",
		"    scaled := v.Shift(p)",
		"    floor := scaled.Floor()",
		"    diff := scaled.Sub(floor)",
		"    switch diff.Cmp(half) {",
		"    case 1:",
		"        return floor.Add(one).Shift(-p)",
		"    case 0:",
		"        if floor.IntPart()%2 == 0 { return floor.Shift(-p) }",
		"        return floor.Add(one).Shift(-p)",
		"    }",
		"    return floor.Shift(-p)",
		"}",
	} {
		b.WriteString(l + "\n")
	}
	for i := 52; i <= 80; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	return b.String()
}

// RequestsSession is the Requests view's session, pricing-rounding, at now with its project in dir: Claude working on "Negative
// values round the wrong way" at round.go:46, a question waiting on you, a request queued behind the turn, a btw, two
// resolved, and the tasks and runs behind them.
func RequestsSession(now time.Time, dir string) core.SessionState {
	ago := func(min int) time.Time { return now.Add(-time.Duration(min) * time.Minute) }
	liam := "Liam Galvin"
	t1 := core.Thread{ID: "7d45e1a0", Title: "Negative values round the wrong way", Status: core.ThreadOpen, Busy: true, Active: true, Created: ago(4), Updated: ago(1),
		Anchor: &core.Anchor{Project: "pricing", File: "internal/pricing/round.go", Line: 46, Text: "    case 0:"},
		Comments: []core.Comment{
			{ID: "c1", Author: core.AuthorYou, Name: liam, Time: ago(4), Delivered: true, Images: []string{"invoice-diff.png"},
				Body: "Ties go to even, but what about negative values? `-2.5` should give `-2`, and `Floor` goes the wrong way for negatives.\n\nWhile you're in here:\n\n- make the negative cases table-driven alongside the positive ones\n- check `RoundMinor` callers in `billing/invoice.go` — I suspect they pre-round\n- don't change the exported signature"},
			{ID: "c2", Author: core.AuthorAgent, Name: "claude", Model: "opus · high", Time: ago(2),
				Body:     "Right — `Floor` rounds toward -∞, so `-2.5` floors to `-3` and the tie check then picks the odd neighbour. Switching to `Truncate` with sign handling, and the negative cases are in the table now.",
				Evidence: []core.Evidence{{Kind: "test-run", Ref: "40ab", Label: "14 passed", OK: true}}},
		},
		Touched: []core.Touched{{Project: "pricing", Path: "internal/pricing/round.go", Added: 7, Removed: 1},
			{Project: "pricing", Path: "internal/pricing/round_test.go", Added: 18}}}
	threads := []core.Thread{t1,
		{ID: "2b81c3d4", Status: core.ThreadOpen, Created: ago(9), Updated: ago(4), Anchor: &core.Anchor{File: "PLAN.md", Line: 22},
			Comments: []core.Comment{
				{ID: "c3", Author: core.AuthorYou, Name: liam, Body: "Move currency precision table to config?", Delivered: true, Time: ago(9)},
				{ID: "c4", Author: core.AuthorAgent, Name: "claude", Body: "Two options — a TOML table loaded at start, or the currencies package's own table.", Time: ago(5)},
				{ID: "c5", Author: core.AuthorAgent, Name: "claude", Body: "Which would you rather?", Time: ago(4)},
			}},
		{ID: "5f02aa11", Status: core.ThreadOpen, Created: ago(2), Updated: ago(2),
			Comments: []core.Comment{{ID: "c6", Author: core.AuthorYou, Name: liam, Body: "Add a fuzz test for RoundMinor", Time: ago(2)}}},
		{ID: "9c3e77b0", Status: core.ThreadOpen, Btw: true, Created: ago(9), Updated: ago(9),
			Comments: []core.Comment{
				{ID: "c7", Author: core.AuthorYou, Name: liam, Body: "btw — why decimal and not big.Rat?", Btw: true, Time: ago(9)},
				{ID: "c8", Author: core.AuthorAgent, Name: "claude", Body: "decimal keeps the scale, so money prints as written.", Btw: true, Time: ago(9)},
			}},
		{ID: "e4d2c1b0", Status: core.ThreadResolved, Created: ago(30), Updated: ago(12), Anchor: &core.Anchor{File: "internal/pricing/tiers.go", Line: 12},
			Comments: []core.Comment{{ID: "c9", Author: core.AuthorYou, Name: liam, Body: "Use banker's rounding for JPY", Time: ago(30)}}},
		{ID: "a1b2c3d4", Status: core.ThreadResolved, Created: ago(40), Updated: ago(19), Anchor: &core.Anchor{File: "internal/pricing/round.go", Line: 8},
			Comments: []core.Comment{{ID: "c10", Author: core.AuthorYou, Name: liam, Body: "Rename Mode → RoundingMode", Time: ago(40)}}},
	}
	se := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindWork, Status: core.StatusWorking,
		Projects: []core.Project{{Name: "pricing", Dir: dir, Branch: "rethink/rounding", Primary: true}},
		Agent: core.AgentState{Provider: "claude", Model: "opus", Effort: "high", Running: true, Working: true, TurnStarted: now.Add(-41 * time.Second),
			Stats: core.Stats{Context: 44_000, ContextMax: 200_000, Input: 20_000, CacheRead: 210_000, Output: 9_000, Turns: 6, ToolCalls: 31, Cost: 1.84}},
		Threads: threads,
		Tasks: []core.Task{
			{ID: "1", Subject: "Add RoundingMode type", Status: core.TaskCompleted},
			{ID: "2", Subject: "Half-even for minor units", Status: core.TaskCompleted},
			{ID: "3", Subject: "Negative tie handling", Status: core.TaskInProgress, Thread: t1.ID},
			{ID: "4", Subject: "Currency precision table", Status: core.TaskPending},
			{ID: "5", Subject: "Commit + PR draft", Status: core.TaskPending},
		},
		Runs: []core.TestRun{{ID: "40ab", Command: "go test ./internal/pricing/", By: "agent", Passed: 14, OK: true, Thread: t1.ID,
			Started: now.Add(-3 * time.Minute), Ended: now.Add(-170 * time.Second), Output: "ok  \tpricing\t0.208s"},
			{ID: "41cd", Command: "go test ./internal/pricing/ -run TestRoundHalfEven -v", By: "you", Passed: 13, Failed: 1, Failing: []string{"TestRoundHalfEven/-3.5@0"},
				Started: now.Add(-time.Minute), Ended: now.Add(-50 * time.Second),
				Output: "=== RUN   TestRoundHalfEven\n--- FAIL: TestRoundHalfEven/-3.5@0 (0.00s)\n    round_test.go:33: got -3, want -4\nFAIL\tpricing\t0.212s"}},
	}
	return se
}

// ClaudeMaxUsage is Claude's plan usage the mockups show: 38% of the 5h window, 71% of the week.
var ClaudeMaxUsage = core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "Claude Max", Windows: []core.UsageWindow{
	{Name: "5h window", Used: .38}, {Name: "weekly", Used: .71}}}}
