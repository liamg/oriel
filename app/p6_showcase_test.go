//go:build showcase

package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// showExplainer answers like an agent would, by what it's asked about and how deep.
func showExplainer(_ context.Context, r registry.ExplainRequest) (string, error) {
	switch {
	case r.Kind == "test-failure":
		return "`RoundHalfEven(-3.5)` returned **-3**, the test wants **-4**.\n\n" +
			"On a tie the code adds `sign(v)` only when the integer part is odd, but `IntPart()` of a negative " +
			"truncates towards zero, so `-3` reads as odd and the tie goes the wrong way. Compare `t.Abs()` instead.", nil
	case r.Kind == "diagnostic":
		return "`greting` is a typo: nothing in package `greet` declares it, so the build stops here. You meant `greeting`, as on line 9.", nil
	case r.Kind == "ci-failure":
		return "- `TestUsageBackoff` wants `Retry-After: 2`, gets `1`.\n" +
			"- The reset is rounded **down** to whole seconds.\n- Round up with `math.Ceil` in `retryAfter()`.", nil
	case r.Depth == registry.ExplainDetailed:
		return "On a tie (`diff == half`) the old code always floored, which biased totals downwards.\n\n" +
			"Now it looks at the integer part: if it's **even** the tie rounds towards it (`t.Shift(-p)`), otherwise it " +
			"steps away from zero with `sign(v)`. That's banker's rounding: over many invoice lines the ups and downs cancel.\n\n" +
			"`diff` is worked out once above the switch, so the comparison and this branch agree.", nil
	case r.Depth == registry.ExplainWhy:
		return "Added in **round half to even** (Claude, this branch) for ENG-2231: finance reported month totals drifting by a cent " +
			"against the ledger. The thread on L12 settled on half-to-even over half-up.", nil
	}
	return "On an exact tie this rounds to the **even** neighbour: `2.5 → 2`, `3.5 → 4`. Before, every tie went down " +
		"(`t.Floor()`), which skewed sums of many lines.", nil
}

// showLedger is a review ledger as 18 has it: 38 of search-api's 59 agent-written lines read, 21 of them uncommitted
// and unread.
func showLedger(dir string) registry.ReviewLedger {
	return registry.ReviewLedger{Name: "showcase", Order: -1, Get: func(root string) registry.Ledger {
		l := registry.Ledger{Root: root, Ready: true}
		if root != dir {
			return l
		}
		for _, f := range []registry.LedgerFile{
			{Path: "internal/api/usage/handler.go", All: registry.LedgerCount{Read: 4}, Uncommitted: registry.LedgerCount{Read: 4}},
			{Path: "internal/api/usage/handler_test.go", All: registry.LedgerCount{Unread: 18}, Uncommitted: registry.LedgerCount{Unread: 18}},
			{Path: "internal/api/usage/routes.go", All: registry.LedgerCount{Read: 17, Unread: 3}, Uncommitted: registry.LedgerCount{Read: 17, Unread: 3}},
			{Path: "internal/ratelimit/reservation.go", All: registry.LedgerCount{Read: 17}},
		} {
			l.Files = append(l.Files, f)
			l.All, l.Uncommitted = l.All.Add(f.All), l.Uncommitted.Add(f.Uncommitted)
		}
		return l
	}}
}

// showSession puts the scene in a work session on dir, in front, as the window has it while an agent works there.
func showSession(t *testing.T, s *errScene, name, project, dir string) {
	t.Helper()
	se := core.SessionState{ID: core.SessionID(name), Name: name, Kind: core.KindWork, Dir: dir, Status: core.StatusNeedsYou,
		Projects: []core.Project{{Name: project, Repo: dir, Dir: dir, Branch: "rethink/rounding", Primary: true}},
		Agent:    core.AgentState{Provider: "claude"}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.frame()
}

// shipShowcase is 18's session with the review ledger counted and web-dashboard's CI failing.
func shipShowcase(t *testing.T) (*errScene, core.SessionState) {
	s, se := mockup18(t)
	s.reg.Contribute(showLedger(sessionProjectDir(se, "search-api")))
	s.reg.Contribute(registry.Explainer{Name: "show", Explain: showExplainer, Label: func() string { return "fork of codex · knows this session" }})
	r := s.ship().reports[se.ID]
	r.ships[0].PRDraft.Body = "## What\n\nRate-limited usage responses (429) now carry `Retry-After`, from the limiter's reset time " +
		"rounded up to whole seconds. Both the single and batch endpoints set it.\n\n## Why\n\nClients retried 429s at once and in " +
		"lockstep (ENG-2231); the dashboard (web-dashboard #812) now waits as told.\n\n## Testing\n\n- table test of the 429 path\n" +
		"- `go test ./...` passes\n"
	web := &r.ships[1]
	web.PR.Body = "## What\n\nWhen the search API answers 429, the dashboard waits for `Retry-After` before it polls again, " +
		"and says so under the chart.\n\n## Why\n\nINF-2231. Depends on acme/search-api's PR.\n"
	web.Checks[3].Conclusion = "failure"
	web.Checks[3].Actions = true
	web.FailLine = "--- FAIL: TestUsageBackoff (0.02s)  usage_test.go:88: Retry-After = 1, want 2"
	s.run("ship.open", nil)
	s.settleGit(t)
	return s, se
}

func TestShowcaseP6Ship(t *testing.T) {
	s, _ := shipShowcase(t)
	s.shot(t, "p6-01-ship")

	s.clickText(t, "Explain with agent")
	s.explainSettle()
	s.shot(t, "p6-01b-ship-ci-explained")

	g, _ := shipShowcase(t)
	g.click(centre(g.access("Commit").Bounds))
	g.frame()
	g.shot(t, "p6-04-ship-commit-gate")
}

func TestShowcaseP6RaisePR(t *testing.T) {
	s, _ := shipShowcase(t)
	s.run("view.close", registry.Args{"id": ShipView})
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.shot(t, "p6-02-raise-pr")

	d, _ := shipShowcase(t)
	d.run("view.close", registry.Args{"id": ShipView})
	d.run("project.open", registry.Args{"project": "web-dashboard"})
	d.run("project.panel", registry.Args{"name": "pull-request"})
	d.run("ship.pr.open", registry.Args{"project": "web-dashboard"})
	d.frame()
	d.shot(t, "p6-03-pr-checklist")
}

func TestShowcaseP6Diff(t *testing.T) {
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	s.reg.Contribute(registry.Explainer{Name: "show", Explain: showExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.sh.git.wait()
	s.run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	for range 3 {
		s.frame()
		s.diff().wait()
		s.sh.runTodo()
	}
	s.run("diff.hunk.next", nil)
	s.run("diff.review", nil)
	s.shot(t, "p6-05a-diff-split")
	s.run("diff.layout", registry.Args{"layout": "unified"})
	s.shot(t, "p6-05b-diff-unified")

	a := aiReviewScene(t)
	a.shot(t, "p6-06-ai-review")
}

func TestShowcaseP6CommitGated(t *testing.T) {
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.reg.Contribute(registry.ReviewLedger{Name: "showcase", Order: -1, Get: func(root string) registry.Ledger {
		f := registry.LedgerFile{Path: "internal/pricing/round.go", All: registry.LedgerCount{Read: 9, Skimmed: 4, Unread: 17},
			Uncommitted: registry.LedgerCount{Skimmed: 4, Unread: 17}}
		return registry.Ledger{Root: root, Ready: true, All: f.All, Uncommitted: f.Uncommitted, Files: []registry.LedgerFile{f}}
	}})
	s.reg.Options().Set("ledger.gate", registry.LayerUser, registry.At{}, "hard")
	s.run("diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	for range 3 {
		s.frame()
		s.diff().wait()
		s.sh.runTodo()
	}
	s.run("git.commit", registry.Args{"path": r.Dir, "message": "fix(pricing): round half to even", "all": true})
	s.until(t, "the gate", func() bool { s.sh.runTodo(); return strings.Contains(s.frameText(), "COMMIT GATED") })
	s.shot(t, "p6-04b-commit-gated")
}

func TestShowcaseP6GitHub(t *testing.T) {
	s, lw, hub, se, _ := shipScene(t)
	x := s.github()
	ag := lw.fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	ag.Emit(agent.TurnEnd{})
	apiDir := shipDir(se, "search-api")
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nimport \"time\"\n\n" +
		"// Handle says how long to wait.\nfunc Handle(cfg Config) time.Duration {\n" +
		"\twait := cfg.RetryAfter\n\tif wait == 0 {\n\t\twait = 2 * time.Second\n\t}\n\treturn wait\n}\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 9, Body: "Silently defaulting to 2s will hide config mistakes. Can this return an error instead?"})
	hub.AddReview("acme/search-api", pr.Number, gh.Review{User: "priya-k", Association: "MEMBER", State: gh.ReviewChangesRequested})
	s.run("github.sync", nil)
	var tid core.ThreadID
	s.until(t, "the mirrored thread", func() bool {
		x.wait()
		st := s.store.State()
		sess, _ := st.Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Origin == core.OriginGitHub && th.GitHub.Root == root.ID {
				tid = th.ID
			}
		}
		return tid != ""
	})
	ag.WaitSent(t)
	ag.Call(t, "reply", map[string]any{"thread": string(tid),
		"body": "Agreed. `Handle` now returns `ErrNoRetryAfter` when the config leaves it unset, and the handler answers 500 with a log line."})
	ag.Emit(agent.TurnEnd{})
	s.run("diff.open", registry.Args{"path": filepath.Join(apiDir, "internal/api/usage/handler.go")})
	s.until(t, "the GitHub block", func() bool { return strings.Contains(s.frameText(), "↗ Forward to GitHub") })
	s.shot(t, "p6-07-github-thread")
}

func TestShowcaseP6Explain(t *testing.T) {
	s, _, path := explainScene(t)
	showSession(t, s, "pricing-rounding", "pricing", filepath.Dir(path))
	s.reg.Contribute(registry.Explainer{Name: "show", Order: -1, Explain: showExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.shot(t, "p6-08a-explain-marks")
	s.cursorTo(13)
	s.explainKeys("<Space>e")
	s.shot(t, "p6-08b-explain-card")
	s.clickText(t, "DETAILED")
	s.explainSettle()
	s.shot(t, "p6-08c-explain-detailed")
	s.explainKeys("<Esc>")

	s.run("explain.units", nil)
	s.explainSettle()
	s.explainSettle()
	s.shot(t, "p6-08d-changed-units")

	s.cursorTo(22)
	s.keys("Vj")
	s.rightClick(s.textAt(t, "Errorf"))
	s.shot(t, "p6-08e-explain-menu")
}

func TestShowcaseP6Walkthrough(t *testing.T) {
	s, _, _, _ := walkScene(t)
	s.run("explain.change", nil)
	s.walkSettle()
	s.shot(t, "p6-09a-walkthrough")
	s.walkKeys(t, "<Right>")
	s.walkSettle()
	s.shot(t, "p6-09b-walkthrough-step2")
}

func TestShowcaseP6Verify(t *testing.T) {
	s, _, _, _ := verifyScene(t)
	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.until(t, "the tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.click(centre(s.access("Verify…").Bounds))
	s.verifySettle(t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	s.answer(t, "It rounds to -2: the tie check at round.go:42 returns the even one.")
	s.verifySettle(t, "the first verdict", func(f string) bool { return strings.Contains(f, "QUESTION 2") })
	s.answer(t, "No, the old lines are skipped.")
	s.verifySettle(t, "the partial verdict", func(f string) bool { return strings.Contains(f, "QUESTION 3") })
	s.shot(t, "p6-10-verify-quiz")
}

func TestShowcaseP6Twins(t *testing.T) {
	s, dir := testsScene(t)
	showSession(t, s, "pricing-rounding", "pricing", dir)
	s.reg.Contribute(registry.Explainer{Name: "show", Explain: showExplainer, Label: func() string { return "fork of claude · knows this session" }})
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "round_test.go")})
	s.waitHighlights()
	s.run("test.run.file", nil)
	s.runDone(t)
	s.keys("20Gzt")
	s.frame()
	s.shot(t, "p6-11a-test-failure-twins")
	for _, ex := range s.fixTwins() {
		s.click(centre(ex.Bounds))
	}
	s.explainSettle()
	s.shot(t, "p6-11b-test-failure-explained")

	c, root := lspScene(t, "go", "gopls", "main.go")
	showSession(t, c, "greet-typo", "greet", root)
	c.reg.Contribute(registry.Explainer{Name: "show", Explain: showExplainer, Label: func() string { return "fork of claude · knows this session" }})
	c.run("editor.open", registry.Args{"path": filepath.Join(root, "main.go")})
	c.lspSettle()
	c.cursorAt(12, 26)
	c.keyCommand(t, "<Space>ca")
	c.lspSettle()
	c.shot(t, "p6-11c-code-actions-agent")
	c.clickText(t, "Ask the agent to explain")
	c.explainSettle()
	c.shot(t, "p6-11d-diagnostic-explained")
}
