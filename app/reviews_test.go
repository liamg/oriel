package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// reviewScene is a window on a host whose GitHub is the fake, seeded as 46-reviews' panel 1: two asks of you, one
// re-requested with commits since, a team's ask, and one you asked changes on.
func reviewScene(t *testing.T) (*errScene, *localHost, *ghtest.Server, map[string]gh.PR) {
	t.Helper()
	hub := ghtest.New(t)
	hub.AddTeam("acme/platform", "you")
	prs := map[string]gh.PR{}
	prs["ratelimit"] = hub.AddPR("acme/api", gh.PR{Title: "Rate-limit the search endpoint per API key", Author: "mkhan", Reviewers: []string{"you"},
		Head: "mk/search-ratelimit", Base: "main", Body: "Adds a token bucket per API key."})
	hub.SetPRSize("acme/api", prs["ratelimit"].Number, ghtest.PRSize{Author: "mkhan", Additions: 214, Deletions: 38,
		Files: []gh.PRFile{{Path: "internal/ratelimit/bucket.go", Additions: 96}}})
	prs["invoices"] = hub.AddPR("acme/billing-ui", gh.PR{Title: "Move invoice PDFs to the render worker", Author: "jsato", Reviewers: []string{"you"}})
	hub.AddCommit("acme/billing-ui", prs["invoices"].Number, "b1")
	hub.AddReview("acme/billing-ui", prs["invoices"].Number, gh.Review{User: "you", State: gh.ReviewChangesRequested})
	hub.AddCommit("acme/billing-ui", prs["invoices"].Number, "b2")
	prs["deps"] = hub.AddPR("acme/pricing", gh.PR{Title: "chore(deps): sweep", Author: "oriel-bot", Reviewers: []string{"acme/platform"}})
	prs["s3"] = hub.AddPR("acme/api", gh.PR{Title: "Retry flaky S3 uploads with jitter", Author: "bnoor"})
	hub.AddReview("acme/api", prs["s3"].Number, gh.Review{User: "you", State: gh.ReviewChangesRequested})
	if err := hub.Client().RequestReviewers(context.Background(), gh.Repo{Owner: "acme", Name: "billing-ui"}, prs["invoices"].Number, []string{"you"}); err != nil {
		t.Fatal(err)
	}
	lw := newHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	return s, lw, hub, prs
}

func (s *errScene) reviews() *reviewsPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*reviewsPlugin); ok {
			return x
		}
	}
	return nil
}

func (s *errScene) launcherPlugin() *launcher {
	for _, e := range s.plugins {
		if x, ok := e.(*launcher); ok {
			return x
		}
	}
	return nil
}

// ui.md, "46 · Reviews": "Groups: Waiting on you (oldest ask first), then In progress." The header counts both, and
// the sidebar's pill is how many wait on you: new, re-requested and (with reviews.count_teams on) a team's ask.
func TestReviewsPage(t *testing.T) {
	s, _, _, prs := reviewScene(t)
	s.run("reviews.open", nil)
	s.until(t, "the requests", func() bool { return strings.Contains(s.frameText(), "Retry flaky S3") })
	text := strings.ReplaceAll(s.frameText(), "\n", " ")
	for _, want := range []string{"REVIEWS", "3 waiting on you · 1 in progress", "WAITING ON YOU", "IN PROGRESS", "RE-REQUESTED", "CHANGES REQUESTED",
		"1 commit since", "@ACME/PLATFORM", "acme/api#" + itoa(prs["ratelimit"].Number)} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %q:\n%s", want, text)
		}
	}
	at := -1
	for _, want := range []string{"Rate-limit the search endpoint", "chore(deps): sweep", "Move invoice PDFs", "Retry flaky S3"} {
		i := strings.Index(text, want)
		if i < at {
			t.Errorf("%q isn't in order:\n%s", want, text)
		}
		at = i
	}
	x := s.reviews()
	if n := x.count(); n != 3 {
		t.Errorf("the pill says %d, want 3", n)
	}
	if err := s.reg.Options().Set("reviews.count_teams", registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	if n := x.count(); n != 2 {
		t.Errorf("without teams the pill says %d, want 2", n)
	}
	s.keyCommand(t, "t")
	s.frame()
	if text := s.frameText(); !strings.Contains(text, "Rate-limit the search") || strings.Contains(text, "chore(deps): sweep") {
		t.Errorf("to me:\n%s", text)
	}
	s.keyCommand(t, "t")
	s.frame()
	if text := s.frameText(); strings.Contains(text, "Rate-limit the search") || !strings.Contains(text, "chore(deps): sweep") {
		t.Errorf("my teams:\n%s", text)
	}
}

// ui.md, "46 · Reviews": the selected PR's detail from its source: branches, size, files, description.
func TestReviewsPageDetail(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	s.run("reviews.open", nil)
	s.until(t, "the detail", func() bool { return strings.Contains(s.frameText(), "internal/ratelimit/bucket.go") })
	text := strings.ReplaceAll(s.frameText(), "\n", " ")
	for _, want := range []string{"mkhan wants to merge mk/search-ratelimit → main", "Adds a token bucket per API key.", "Start review…",
		"Open in GitHub", "you", "REQUESTED", "no checkout of acme/api is registered"} {
		if !strings.Contains(text, want) {
			t.Errorf("the detail lacks %q:\n%s", want, text)
		}
	}
	s.golden("reviews-page")
}

// ui.md, "46 · Reviews": a PR with no registered checkout offers Add repo…, the Add repo dialog on the source's clone URL.
func TestReviewsAddRepo(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	s.run("reviews.open", nil)
	s.until(t, "the detail", func() bool { return strings.Contains(s.frameText(), "no checkout of acme/api is registered") })
	s.clickText(t, "Add repo…")
	s.until(t, "the clone target", func() bool {
		s.now = s.now.Add(100 * time.Millisecond)
		return strings.Contains(s.frameText(), "Clones into")
	})
	s.wantFrame(t, "Clone from URL", "https://github.com/acme/api.git", "api")
}

// ui.md, "46 · Reviews": "Start review… = session.new {kind = review, pr = url}: the launcher on Review PR with the PR
// filled in"; "Continue review… = session.open {id} for the session whose PR is this URL". Both close the page.
func TestReviewsStartAndContinue(t *testing.T) {
	s, lw, _, prs := reviewScene(t)
	x := s.reviews()
	s.run("reviews.open", nil)
	s.until(t, "the requests", func() bool { return strings.Contains(s.frameText(), "Rate-limit the search") })
	s.keyCommand(t, "<CR>")
	s.frame()
	l := s.launcherPlugin()
	s.until(t, "the launcher's kinds", func() bool { _, ok := l.current(); return ok })
	k, _ := l.current()
	if x.page.open || !l.open || l.pr != prs["ratelimit"].URL || k.Kind != core.KindReview {
		t.Fatalf("Start review: page open %v, launcher open %v on %q with %q", x.page.open, l.open, k.Kind, l.pr)
	}
	s.typeKeys("Esc")
	s.frame()

	for _, args := range [][]string{{"remote", "add", "origin", lw.repo}, {"update-ref", fmt.Sprintf("refs/pull/%d/head", prs["ratelimit"].Number), "HEAD"}} {
		if out, err := exec.Command("git", append([]string{"-C", lw.repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	id, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindReview, PR: prs["ratelimit"].URL, Repos: []core.RepoID{core.RepoID(lw.repo)},
		Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, s.store, "the review session", func(st core.State) bool { _, ok := st.Session(id); return ok })
	s.run("reviews.open", nil)
	s.until(t, "reviewing", func() bool { return strings.Contains(s.frameText(), "REVIEWING") })
	s.clickText(t, "Rate-limit the search endpoint per API key")
	s.frame()
	if !strings.Contains(s.frameText(), "Continue review…") {
		t.Errorf("no Continue review…:\n%s", s.frameText())
	}
	if strings.Contains(s.frameText(), "3 waiting on you") {
		t.Error("a request with a session still counts as waiting")
	}
	s.keyCommand(t, "<CR>")
	s.frame()
	waitState(t, s.store, "the review in front", func(st core.State) bool { return st.Focus == id })
	if x.page.open {
		t.Error("the page stayed open over the session")
	}
}

// ui.md, "46 · Reviews": "A PR that wasn't in the last list and counts is a notice: 'mkhan asked you to review api#812'".
func TestReviewsNoticeOnANewAsk(t *testing.T) {
	s, _, hub, _ := reviewScene(t)
	x := s.reviews()
	s.run("reviews.open", nil)
	s.until(t, "the requests", func() bool { return strings.Contains(s.frameText(), "Rate-limit the search") })
	if n := len(s.store.State().Notifications); n != 0 {
		t.Errorf("%d notices for the first list", n)
	}
	pr := hub.AddPR("acme/gateway", gh.PR{Title: "Drop the legacy v1 webhooks", Author: "rlopez", Reviewers: []string{"you"}})
	<-x.fresh(s.sh.sites.reviewSources()[0], 0)
	waitState(t, s.store, "the notice", func(st core.State) bool {
		for _, n := range st.Notifications {
			if n.Text == "rlopez asked you to review gateway#"+itoa(pr.Number) {
				return true
			}
		}
		return false
	})
}

// The page closes with Esc and ✕, opened alone and over Settings and the omnibox.
func TestReviewsPageCloses(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	x := s.reviews()
	for _, over := range []string{"", "Settings", "omnibox"} {
		for _, how := range []string{"Esc", "✕"} {
			switch over {
			case "Settings":
				s.run("Settings", nil)
			case "omnibox":
				s.run("omnibox.open", nil)
			}
			s.frame()
			s.run("reviews.open", nil)
			s.until(t, "the page", func() bool { return strings.Contains(s.frameText(), "WAITING ON YOU") })
			close := func() {
				if how == "Esc" {
					s.typeKeys("Esc")
				} else {
					s.click(centre(s.within("Reviews page", "✕").Bounds))
				}
				s.frame()
			}
			close()
			if over == "omnibox" {
				if s.omni().open || !x.page.open {
					t.Errorf("%s over the omnibox: omnibox open %v, page open %v", how, s.omni().open, x.page.open)
				}
				close()
			}
			if x.page.open {
				t.Errorf("opened over %q, %s didn't close it", over, how)
				x.page.open = false
			}
		}
	}
}

// The assistant's review_requests reads every source, with each request's state and the team asked.
func TestReviewRequestsForTheAssistant(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	out, err := toolNamed(t, s, "review_requests").Run(context.Background(), map[string]any{})
	got, _ := out.([]reviewLine)
	if err != nil || len(got) != 4 {
		t.Fatalf("review_requests: %+v %v", out, err)
	}
	if got[0].Title != "Rate-limit the search endpoint per API key" || got[0].State != registry.ReviewNew || got[0].Source != "GitHub" ||
		got[1].Via != "@acme/platform" || got[2].State != registry.ReviewReRequested || got[3].State != registry.ReviewChanges {
		t.Errorf("review_requests: %+v", got)
	}
}

func toolNamed(t *testing.T, s *errScene, name string) registry.AgentTool {
	t.Helper()
	for _, tl := range registry.AgentToolsFor(s.reg, assistantAgent, true) {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool %s", name)
	return registry.AgentTool{}
}

const gitlabLua = `
oriel.code.host{ name = "gitlab", title = "GitLab", order = 20, takes = [[/-/merge_requests/\d+]],
  sites = { { host = "gitlab.com", api = "https://gitlab.com/api/v4" } }, path = [[^/?(.+?)(?:\.git)?/?$]],
  requested = function(site, done)
    done({ { url = "https://gitlab.com/acme/gateway/-/merge_requests/57", repo = "acme/gateway", number = 57,
      title = "Drop the legacy v1 webhooks", author = "rlopez", asked = "2026-10-19T09:00:00Z", adds = 12, dels = 1204 } })
  end,
  checkout = function(site, url, done) done({ remote = "acme/gateway", ref = "merge-requests/57/head" }) end }
`

// reviews.max_age leaves out requests asked longer ago, on the page and in the pill alike; 0 shows them all, and a
// request with no time asked is always shown.
func TestOldReviewRequestsLeftOut(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	s.now = at(12, 0, 0)
	m, err := plugin.ParseManifest([]byte("id = \"acme/gitlab\"\nversion = \"0.1.0\"\ndescription = \"A GitLab-like source\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	ask := func(n int, title, asked string) string {
		return fmt.Sprintf(`{ url = "https://gitlab.com/acme/gateway/-/merge_requests/%d", repo = "acme/gateway", number = %d, title = %q, author = "rlopez"%s }`,
			n, n, title, asked)
	}
	src := `oriel.code.host{ name = "gitlab", title = "GitLab", order = 20, takes = [[/-/merge_requests/\d+]],
  sites = { { host = "gitlab.com", api = "https://gitlab.com/api/v4" } }, path = [[^/?(.+?)(?:\.git)?/?$]],
  requested = function(site, done) done({ ` + ask(1, "Recent ask", `, asked = "2026-10-11T09:00:00Z"`) + `, ` +
		ask(2, "Ancient ask", `, asked = "2026-08-01T09:00:00Z"`) + `, ` + ask(3, "Undated ask", "") + ` }) end }`
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(src)}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	s.run("reviews.open", nil)
	s.until(t, "GitLab's", func() bool { return strings.Contains(s.frameText(), "Recent ask") })
	titles := func() (out []string) {
		for _, r := range s.reviews().rows() {
			if r.req != nil && strings.Contains(r.req.URL, "gitlab.com") {
				out = append(out, r.req.Title)
			}
		}
		slices.Sort(out)
		return out
	}
	if got := titles(); !slices.Equal(got, []string{"Recent ask", "Undated ask"}) {
		t.Errorf("with reviews.max_age 30d: %v", got)
	}
	if strings.Contains(s.frameText(), "Ancient ask") {
		t.Error("the page lists a request asked 73 days ago")
	}
	if err := s.reg.Options().Set("reviews.max_age", registry.LayerUser, registry.At{}, time.Duration(0)); err != nil {
		t.Fatal(err)
	}
	if got := titles(); len(got) != 3 {
		t.Errorf("with reviews.max_age 0: %v", got)
	}
}

// api.md, "Lua": oriel.code.host brings a code host; its requests list beside GitHub's with a source chip, and
// a review of one fetches what its checkout says (Phase 2: "the source resolves checkout").
func TestLuaReviewSource(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	m, err := plugin.ParseManifest([]byte("id = \"acme/gitlab\"\nversion = \"0.1.0\"\ndescription = \"A GitLab-like source\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(gitlabLua)}); err != nil {
		t.Fatal(err)
	}
	s.frame()
	s.run("reviews.open", nil)
	s.until(t, "GitLab's", func() bool { return strings.Contains(s.frameText(), "Drop the legacy v1 webhooks") })
	if text := s.frameText(); !strings.Contains(text, "GITLAB") || !strings.Contains(text, "acme/gateway#57") {
		t.Errorf("GitLab's row:\n%s", text)
	}
	spec := core.SessionSpec{Kind: core.KindReview, PR: "https://gitlab.com/acme/gateway/-/merge_requests/57"}
	done := make(chan error, 1)
	go func() { done <- s.sh.sessions.prCheckout(context.Background(), &spec) }() // off the loop, as session.start runs
	s.until(t, "the checkout", func() bool {
		select {
		case err = <-done:
			return true
		default:
			return false
		}
	})
	if err != nil || spec.PRRemote != "acme/gateway" || spec.PRRef != "merge-requests/57/head" {
		t.Errorf("checkout: %+v %v", spec, err)
	}
	gh := core.SessionSpec{Kind: core.KindReview, PR: "https://github.com/acme/api/pull/7"}
	if err := s.sh.sessions.prCheckout(context.Background(), &gh); err != nil || gh.PRRemote != "acme/api" || gh.PRRef != "pull/7/head" {
		t.Errorf("GitHub's checkout: %+v %v", gh, err)
	}
}

// docs/examples/gitea: "an error in a later callback hangs silently". A code host's answer that a later callback
// (an exec's on_done, an http.request's) would have given fails with that callback's error, at once.
func TestLuaCodeHostCallbackError(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	m, err := plugin.ParseManifest([]byte("id = \"acme/broken\"\nversion = \"0.1.0\"\ndescription = \"x\"\n[permissions]\nexec = { commands = [\"true\"] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.pluginVM(t, plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	src := `oriel.code.host{ name = "broken", sites = { { host = "broken.dev" } },
  requested = function(site, done)
    oriel.exec({ "true" }, { on_done = function() local rows = nil; for _ in ipairs(rows) do end; done({}) end })
  end }`
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(src)}); err != nil {
		t.Fatal(err)
	}
	h, ok := registry.HostByName(s.reg, "broken")
	if !ok {
		t.Fatal("the host didn't register")
	}
	got := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	go func() { _, err := h.Requested(ctx, registry.Site{Host: "broken.dev"}); got <- err }()
	s.until(t, "the answer", func() bool {
		select {
		case err = <-got:
			return true
		default:
			return false
		}
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "ipairs") {
		t.Errorf("requested = %v, want the callback's error", err)
	}
}
