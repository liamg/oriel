//go:build showcase

package ship_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// shipShowcase is the Ship session with web-dashboard's CI failing.
func shipShowcase(t *testing.T) (*apptest.Scene, core.SessionState) {
	s, se := shipSession(t)
	s.Registry().Contribute(registry.Explainer{Name: "show", Explain: rig.ShowExplainer, Label: func() string { return "fork of codex · knows this session" }})
	shipOf(s).Fixture(se.ID, false, func(ships []ship.Read) []ship.Read {
		ships[0].PRDraft.Body = "## What\n\nRate-limited usage responses (429) now carry `Retry-After`, from the limiter's reset time " +
			"rounded up to whole seconds. Both the single and batch endpoints set it.\n\n## Why\n\nClients retried 429s at once and in " +
			"lockstep (ENG-2231); the dashboard (web-dashboard #812) now waits as told.\n\n## Testing\n\n- table test of the 429 path\n" +
			"- `go test ./...` passes\n"
		web := &ships[1]
		web.PR.Body = "## What\n\nWhen the search API answers 429, the dashboard waits for `Retry-After` before it polls again, " +
			"and says so under the chart.\n\n## Why\n\nINF-2231. Depends on acme/search-api's PR.\n"
		web.Checks[3].State = registry.CheckFailed
		web.FailLine = "--- FAIL: TestUsageBackoff (0.02s)  usage_test.go:88: Retry-After = 1, want 2"
		return ships
	})
	s.Run("ship.open", nil)
	s.SettleGit()
	return s, se
}

func TestShowcaseP6Ship(t *testing.T) {
	s, _ := shipShowcase(t)
	s.Shot("p6-01-ship")

	s.ClickText("Explain with agent")
	s.ExplainSettle()
	s.Shot("p6-01b-ship-ci-explained")

	g, _ := shipShowcase(t)
	g.Click(apptest.Centre(g.Node("Commit").Bounds), 0)
	g.Frame()
	g.Shot("p6-04-ship-commit-gate")
}

func TestShowcaseP6RaisePR(t *testing.T) {
	s, _ := shipShowcase(t)
	s.Run("view.close", registry.Args{"id": kit.ShipView})
	s.Run("project.panel", registry.Args{"name": "pull-request"})
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Shot("p6-02-raise-pr")

	d, _ := shipShowcase(t)
	d.Run("view.close", registry.Args{"id": kit.ShipView})
	d.Run("project.open", registry.Args{"project": "web-dashboard"})
	d.Run("project.panel", registry.Args{"name": "pull-request"})
	d.Run("ship.pr.open", registry.Args{"project": "web-dashboard"})
	d.Frame()
	d.Shot("p6-03-pr-checklist")
}

func TestShowcaseP6GitHub(t *testing.T) {
	s, lw, hub, se, _ := shipScene(t)
	x := githubOf(s)
	ag := lw.Fake.WaitStarted(t)
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
	s.Run("github.sync", nil)
	var tid core.ThreadID
	s.Until("the mirrored thread", func() bool {
		x.Wait()
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Origin == core.OriginGitHub && th.Hosted.Root == root.ID {
				tid = th.ID
			}
		}
		return tid != ""
	})
	ag.WaitSent(t)
	ag.Call(t, "reply", map[string]any{"thread": string(tid),
		"body": "Agreed. `Handle` now returns `ErrNoRetryAfter` when the config leaves it unset, and the handler answers 500 with a log line."})
	ag.Emit(agent.TurnEnd{})
	s.Run("diff.open", registry.Args{"path": filepath.Join(apiDir, "internal/api/usage/handler.go")})
	s.Until("the GitHub block", func() bool { return strings.Contains(s.Text(), "↗ Forward to GitHub") })
	s.Shot("p6-07-github-thread")
}

func TestShowcaseP6Verify(t *testing.T) {
	s, _, _, _ := verifyScene(t)
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.Click(apptest.Centre(s.Node("Verify…").Bounds), 0)
	verifySettle(s, t, "the first question", func(f string) bool { return strings.Contains(f, "QUESTION 1") })
	answer(s, t, "It rounds to -2: the tie check at round.go:42 returns the even one.")
	verifySettle(s, t, "the first verdict", func(f string) bool { return strings.Contains(f, "QUESTION 2") })
	answer(s, t, "No, the old lines are skipped.")
	verifySettle(s, t, "the partial verdict", func(f string) bool { return strings.Contains(f, "QUESTION 3") })
	s.Shot("p6-10-verify-quiz")
}
