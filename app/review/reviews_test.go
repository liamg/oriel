package review_test

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/app/kit"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/review"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// reviewScene is a window on a host whose GitHub is the fake, seeded with two asks of you, one
// re-requested with commits since, a team's ask, and one you asked changes on.
func reviewScene(t *testing.T) (*apptest.Scene, *rig.Host, *ghtest.Server, map[string]gh.PR) {
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
	lw := rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	state := t.TempDir()
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		apptest.Connect(lw.W)(cfg)
		cfg.StateDir = state // the layout and what plugins keep, as a window's are
	}})
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := s.Registry().Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	return s, lw, hub, prs
}

func reviewsOf(s *apptest.Scene) *review.Reviews {
	return apptest.PluginOf[*review.Reviews](s, "reviews")
}

func prReviewOf(s *apptest.Scene) *review.PRReview {
	return apptest.PluginOf[*review.PRReview](s, "pr-review")
}

// Reviews are grouped Waiting on you (oldest ask first), then In progress. The header counts both, and
// the sidebar's pill is how many wait on you: new, re-requested and (with reviews.count_teams on) a team's ask.
func TestReviewsPage(t *testing.T) {
	s, _, _, prs := reviewScene(t)
	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Retry flaky S3") })
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	for _, want := range []string{"REVIEWS", "3 waiting on you · 1 in progress", "WAITING ON YOU", "IN PROGRESS", "RE-REQUESTED", "CHANGES REQUESTED",
		"1 commit since", "@ACME/PLATFORM", "acme/api#" + strconv.Itoa(prs["ratelimit"].Number)} {
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
	x := reviewsOf(s)
	if n := x.Count(); n != 3 {
		t.Errorf("the pill says %d, want 3", n)
	}
	if err := s.Registry().Options().Set("reviews.count_teams", registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	if n := x.Count(); n != 2 {
		t.Errorf("without teams the pill says %d, want 2", n)
	}
	s.Press("t")
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "Rate-limit the search") || strings.Contains(text, "chore(deps): sweep") {
		t.Errorf("to me:\n%s", text)
	}
	s.Press("t")
	s.Frame()
	if text := s.Text(); strings.Contains(text, "Rate-limit the search") || !strings.Contains(text, "chore(deps): sweep") {
		t.Errorf("my teams:\n%s", text)
	}
}

// The selected PR's detail from its source: branches, its status in the Ship panel's boxes, size, files, description.
func TestReviewsPageDetail(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	s.Run("reviews.open", nil)
	s.Until("the detail", func() bool { return strings.Contains(s.Text(), "internal/ratelimit/bucket.go") })
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	for _, want := range []string{"mkhan wants to merge mk/search-ratelimit → main", "Adds a token bucket per API key.", "Start review…",
		"Open in GitHub", "you", "REQUESTED", "no checkout of acme/api is registered", "OPEN", "CHECKS", "No checks reported",
		"PULL REQUEST", "Mergeable", "REVIEWS", "waiting", "checked just now"} {
		if !strings.Contains(text, want) {
			t.Errorf("the detail lacks %q:\n%s", want, text)
		}
	}
	s.Golden("reviews-page")
}

// The selected PR's activity, oldest first, under its description: pushes, reviews, and comments in its conversation
// and on its lines; a review with no body of its own is left to its line comments. Reactions show under the PR's
// description and each comment, in GitHub's order, those with none left out.
func TestReviewsPageActivity(t *testing.T) {
	s, _, hub, prs := reviewScene(t)
	n := prs["ratelimit"].Number
	hub.SetPRReactions("acme/api", n, gh.Reactions{"rocket": 1, "+1": 3})
	hub.AddIssueComment("acme/api", n, gh.IssueComment{User: "mkhan", Body: "Ready for a look.", Reactions: gh.Reactions{"eyes": 2}})
	hub.AddReviewComment("acme/api", n, gh.ReviewComment{User: "priya", Body: "Why **1**?", Path: "internal/ratelimit/bucket.go", Line: 3})
	hub.AddCommit("acme/api", n, "a1b2c3d4e5")
	hub.AddReview("acme/api", n, gh.Review{User: "priya", State: gh.ReviewCommented})
	hub.AddReview("acme/api", n, gh.Review{User: "priya", State: gh.ReviewChangesRequested, Body: "Charge the request's cost."})
	s.Run("reviews.open", nil)
	s.Until("the activity", func() bool { return strings.Contains(s.Text(), "ACTIVITY") })
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	want := []string{"DESCRIPTION", "+1", "3", "rocket", "1", "ACTIVITY  4", "mkhan", "commented", "Ready for a look.", "eyes", "2", "priya", "commented on internal/ratelimit/bucket.go:3", "Why",
		"mkhan", "pushed a1b2c3d", "commit a1b2c3d", "priya", "requested changes", "Charge the request's cost."}
	at := 0
	for _, w := range want {
		i := strings.Index(text[at:], w)
		if i < 0 {
			t.Fatalf("the activity lacks %q, in order:\n%s", w, text)
		}
		at += i + len(w)
	}
	if strings.Contains(text, "priya reviewed") {
		t.Errorf("a review with only line comments shows on its own:\n%s", text)
	}
	s.Golden("reviews-activity")
}

// A PR with no registered checkout offers Add repo…, the Add repo dialog on the source's clone URL.
func TestReviewsAddRepo(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	s.Run("reviews.open", nil)
	// The request says it has no checkout at once; Add repo comes with the PR's detail, once its host has answered.
	s.Until("the detail", func() bool {
		return strings.Contains(s.Text(), "no checkout of acme/api is registered") && strings.Contains(s.Text(), "Add repo…")
	})
	s.ClickText("Add repo…")
	s.Until("the clone target", func() bool {
		s.Now = s.Now.Add(100 * time.Millisecond)
		return strings.Contains(s.Text(), "Clones into")
	})
	s.WantFrame("Clone from URL", "https://github.com/acme/api.git", "api")
}

// Starting a review, in front or behind, of a PR whose repo isn't cloned opens Add repo on its clone URL, saying why,
// and starts the review once the repo's added.
func TestReviewsStartOffersToClone(t *testing.T) {
	for _, how := range []string{"start", "start-background"} {
		t.Run(how, func(t *testing.T) {
			s, lw, _, prs := reviewScene(t)
			s.Run("reviews.open", nil)
			s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Rate-limit the search") })
			if how == "start" {
				s.Press("<CR>")
			} else if err := s.Host().Run(context.Background(), "reviews.start-background", registry.Args{"url": prs["ratelimit"].URL}); err != nil {
				t.Fatal(err)
			}
			s.Until("the Add repo dialog", func() bool {
				s.Now = s.Now.Add(100 * time.Millisecond)
				return strings.Contains(s.Text(), "Clones into")
			})
			s.WantFrame("You don't have acme/api cloned", "Clone from URL", "https://github.com/acme/api.git")
			if s.Find("Review PR") != nil {
				t.Error("the launcher opened with no checkout to review in")
			}
			if how == "start" {
				return
			}

			// Adding the checkout, here an existing one, starts the review behind.
			alias := filepath.Join(t.TempDir(), "acme", "api")
			if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(lw.Repo, alias); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"remote", "add", "origin", alias}, {"update-ref", fmt.Sprintf("refs/pull/%d/head", prs["ratelimit"].Number), "HEAD"}} {
				if out, err := exec.Command("git", append([]string{"-C", lw.Repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			s.ClickText("Existing directory")
			s.Frame()
			s.TypeInto("Directory", lw.Repo)
			s.Until("the directory checked", func() bool {
				s.Now = s.Now.Add(100 * time.Millisecond)
				return strings.Contains(s.Text(), "git repo")
			})
			s.Press("<D-CR>")
			s.UntilState("the review started", func(core.State) bool {
				se, ok := review.PRSession(s.Host(), prs["ratelimit"].URL)
				return ok && se.Snapshot().Kind == core.KindReview
			})
			s.Until("the notice to open it", func() bool {
				return strings.Contains(s.Text(), "Reviewing api#"+strconv.Itoa(prs["ratelimit"].Number)+" in the background")
			})
		})
	}
}

// Start review… is session.new {kind = review, pr = url}: the launcher on Review PR with the PR filled in; Continue
// review… is session.open {id} for the session whose PR is this URL. Both close the page.
func TestReviewsStartAndContinue(t *testing.T) {
	s, lw, _, prs := reviewScene(t)
	x := reviewsOf(s)
	alias := filepath.Join(t.TempDir(), "acme", "api")
	if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lw.Repo, alias); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"remote", "add", "origin", alias}, {"update-ref", fmt.Sprintf("refs/pull/%d/head", prs["ratelimit"].Number), "HEAD"}} {
		if out, err := exec.Command("git", append([]string{"-C", lw.Repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Rate-limit the search") })
	s.Press("<CR>")
	s.Frame()
	s.Until("the launcher's kinds", func() bool { return s.Find("Review PR") != nil })
	if kind := s.Node("Review PR"); x.PageOpen() || s.Find("New session") == nil || !kind.Selected || !hasValue(s.Access(), prs["ratelimit"].URL) {
		t.Fatalf("Start review: page open %v, launcher open %v on Review PR %v:\n%s", x.PageOpen(), s.Find("New session") != nil, kind.Selected, s.FullText())
	}
	s.Type("Esc")
	s.Frame()

	id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindReview, PR: prs["ratelimit"].URL, Repos: []core.RepoID{core.RepoID(lw.Repo)},
		Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	rig.WaitState(t, s.Store(), "the review session", func(st core.State) bool { _, ok := st.Session(id); return ok })
	s.Run("reviews.open", nil)
	s.Until("reviewing", func() bool { return strings.Contains(s.Text(), "REVIEWING") })
	s.ClickText("Rate-limit the search endpoint per API key")
	s.Frame()
	if !strings.Contains(s.Text(), "Continue review…") {
		t.Errorf("no Continue review…:\n%s", s.Text())
	}
	if strings.Contains(s.Text(), "3 waiting on you") {
		t.Error("a request with a session still counts as waiting")
	}
	s.Press("<CR>")
	s.Frame()
	rig.WaitState(t, s.Store(), "the review in front", func(st core.State) bool { return st.Focus == id })
	if x.PageOpen() {
		t.Error("the page stayed open over the session")
	}
}

// A PR that wasn't in the last list and counts is a notice: "mkhan asked you to review api#812".
func TestReviewsNoticeOnANewAsk(t *testing.T) {
	s, _, hub, _ := reviewScene(t)
	x := reviewsOf(s)
	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Rate-limit the search") })
	if n := len(s.Store().State().Notifications); n != 0 {
		t.Errorf("%d notices for the first list", n)
	}
	pr := hub.AddPR("acme/gateway", gh.PR{Title: "Drop the legacy v1 webhooks", Author: "rlopez", Reviewers: []string{"you"}})
	<-x.Fresh(kit.SitesOf(s.Registry()).ReviewSources()[0])
	rig.WaitState(t, s.Store(), "the notice", func(st core.State) bool {
		for _, n := range st.Notifications {
			if n.Text == "rlopez asked you to review gateway#"+strconv.Itoa(pr.Number) {
				return true
			}
		}
		return false
	})
}

// A new ask's toast stays until answered: Ignore puts it away, and Start review in background starts a review of it
// on the default agent, in a tab behind the session in front, with a notice to open it.
func TestReviewsNoticeStartsAReviewBehind(t *testing.T) {
	s, lw, hub, _ := reviewScene(t)
	x := reviewsOf(s)
	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Rate-limit the search") })
	s.Type("Esc")
	s.Frame()
	ask := func(title string) gh.PR {
		pr := hub.AddPR("acme/api", gh.PR{Title: title, Author: "rlopez", Reviewers: []string{"you"}})
		<-x.Fresh(kit.SitesOf(s.Registry()).ReviewSources()[0])
		s.Until("the toast", func() bool {
			return s.Has("rlopez asked you to review api#"+strconv.Itoa(pr.Number), "Start review in background", "Ignore")
		})
		return pr
	}
	reviews := func() (n int) {
		for _, se := range s.Store().State().Sessions {
			if se.Kind == core.KindReview {
				n++
			}
		}
		return n
	}

	ask("Drop the legacy v1 webhooks")
	s.ClickText("Ignore")
	s.Until("the toast gone", func() bool { return !strings.Contains(s.Text(), "Start review in background") })
	if n := reviews(); n != 0 {
		t.Fatalf("Ignore started %d reviews", n)
	}

	alias := filepath.Join(t.TempDir(), "acme", "api")
	if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lw.Repo, alias); err != nil {
		t.Fatal(err)
	}
	pr := ask("Cache the search index")
	for _, args := range [][]string{{"remote", "add", "origin", alias}, {"update-ref", fmt.Sprintf("refs/pull/%d/head", pr.Number), "HEAD"}} {
		if out, err := exec.Command("git", append([]string{"-C", lw.Repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	front := s.Store().State().Focus
	s.ClickText("Start review in background")
	s.UntilState("the review in a tab", func(st core.State) bool {
		se, ok := review.PRSession(s.Host(), pr.URL)
		return ok && se.Snapshot().Kind == core.KindReview && slices.Contains(st.Tabs, se.ID())
	})
	st := s.Store().State()
	se, _ := review.PRSession(s.Host(), pr.URL)
	if a := se.Snapshot().Agent; a.Provider != "fake" {
		t.Errorf("the review's agent is %q, want the default, fake", a.Provider)
	}
	if st.Focus != front {
		t.Errorf("the review came to the front (%s), want it behind %q", st.Focus, front)
	}
	s.Until("the notice to open it", func() bool {
		return strings.Contains(s.Text(), "Reviewing api#"+strconv.Itoa(pr.Number)+" in the background")
	})

	// Its tab, clicked, opens it the way starting it in front would: the Explorer on Changed, the PR's diff in front.
	if err := s.Host().Run(context.Background(), "session.focus", registry.Args{"id": string(se.ID())}); err != nil {
		t.Fatal(err)
	}
	s.Until("the window on the review", func() bool { s.RunQueued(); return s.Reopen() })
	onChanges := func() bool {
		return s.Showing(kit.ExplorerView) && s.Showing(kit.DiffView)
	}
	s.Until("the Explorer on Changed and the PR's diff", onChanges)

	// The window opening again on it, on a directory with nothing laid out (its checkout arriving), keeps it there.
	w, _ := kit.WindowOf(s.Registry())
	if err := w.Reopen(t.TempDir(), se.ID()); err != nil {
		t.Fatal(err)
	}
	s.Until("the window opened again", func() bool { s.RunQueued(); return s.Reopen() })
	s.Until("the Explorer and diff after it", onChanges)
}

// The page closes with Esc and ✕, opened alone and over Settings and the omnibox.
func TestReviewsPageCloses(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	x := reviewsOf(s)
	for _, over := range []string{"", "Settings", "omnibox"} {
		for _, how := range []string{"Esc", "✕"} {
			switch over {
			case "Settings":
				s.Run("Settings", nil)
			case "omnibox":
				s.Run("omnibox.open", nil)
			}
			s.Frame()
			s.Run("reviews.open", nil)
			s.Until("the page", func() bool { return strings.Contains(s.Text(), "WAITING ON YOU") })
			close := func() {
				if how == "Esc" {
					s.Type("Esc")
				} else {
					s.Click(apptest.Centre(s.Within("Reviews page", "✕").Bounds), 0)
				}
				s.Frame()
			}
			close()
			if over == "omnibox" {
				if (s.Find("Search everything") != nil) || !x.PageOpen() {
					t.Errorf("%s over the omnibox: omnibox open %v, page open %v", how, (s.Find("Search everything") != nil), x.PageOpen())
				}
				close()
			}
			if x.PageOpen() {
				t.Errorf("opened over %q, %s didn't close it", over, how)
				x.ClosePage()
			}
		}
	}
}

// The assistant's review_requests reads every source, with each request's state and the team asked.
func TestReviewRequestsForTheAssistant(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	out, err := toolNamed(t, s, "review_requests").Run(context.Background(), map[string]any{})
	got, _ := out.([]review.ReviewLine)
	if err != nil || len(got) != 4 {
		t.Fatalf("review_requests: %+v %v", out, err)
	}
	if got[0].Title != "Rate-limit the search endpoint per API key" || got[0].State != registry.ReviewNew || got[0].Source != "GitHub" ||
		got[1].Via != "@acme/platform" || got[2].State != registry.ReviewReRequested || got[3].State != registry.ReviewChanges {
		t.Errorf("review_requests: %+v", got)
	}
}

func toolNamed(t *testing.T, s *apptest.Scene, name string) registry.AgentTool {
	t.Helper()
	for _, tl := range registry.AgentToolsFor(s.Registry(), kit.AssistantAgent, true) {
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
	s.Now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	m, err := plugin.ParseManifest([]byte("id = \"acme/gitlab\"\nversion = \"0.1.0\"\ndescription = \"A GitLab-like source\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
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
	s.Frame()
	s.Run("reviews.open", nil)
	s.Until("GitLab's", func() bool { return strings.Contains(s.Text(), "Recent ask") })
	titles := func() (out []string) {
		out = reviewsOf(s).Titles("gitlab.com")
		slices.Sort(out)
		return out
	}
	if got := titles(); !slices.Equal(got, []string{"Recent ask", "Undated ask"}) {
		t.Errorf("with reviews.max_age 30d: %v", got)
	}
	if strings.Contains(s.Text(), "Ancient ask") {
		t.Error("the page lists a request asked 73 days ago")
	}
	if err := s.Registry().Options().Set("reviews.max_age", registry.LayerUser, registry.At{}, time.Duration(0)); err != nil {
		t.Fatal(err)
	}
	if got := titles(); len(got) != 3 {
		t.Errorf("with reviews.max_age 0: %v", got)
	}
}

// oriel.code.host (docs/api.md) brings a code host; its requests list beside GitHub's with a source chip, and a
// review of one fetches what its checkout says.
func TestLuaReviewSource(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	m, err := plugin.ParseManifest([]byte("id = \"acme/gitlab\"\nversion = \"0.1.0\"\ndescription = \"A GitLab-like source\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(gitlabLua)}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	s.Run("reviews.open", nil)
	s.Until("GitLab's", func() bool { return strings.Contains(s.Text(), "Drop the legacy v1 webhooks") })
	if text := s.Text(); !strings.Contains(text, "GITLAB") || !strings.Contains(text, "acme/gateway#57") {
		t.Errorf("GitLab's row:\n%s", text)
	}
	spec := core.SessionSpec{Kind: core.KindReview, PR: "https://gitlab.com/acme/gateway/-/merge_requests/57"}
	done := make(chan error, 1)
	go func() { done <- prReviewOf(s).Checkout(context.Background(), &spec) }() // off the loop, as session.start runs
	s.Until("the checkout", func() bool {
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
	if err := prReviewOf(s).Checkout(context.Background(), &gh); err != nil || gh.PRRemote != "acme/api" || gh.PRRef != "pull/7/head" {
		t.Errorf("GitHub's checkout: %+v %v", gh, err)
	}
	// A review is named after its pull request, as its host writes one, so two reviews' tabs tell apart.
	for _, sp := range []*core.SessionSpec{&spec, &gh} {
		prReviewOf(s).Name(sp)
	}
	if spec.Title != "gateway#57" || spec.Name != "gateway-57" || gh.Title != "api#7" || gh.Name != "api-7" {
		t.Errorf("named %q (%q) and %q (%q), want gateway#57 and api#7", spec.Title, spec.Name, gh.Title, gh.Name)
	}
}

// hasValue says a node at or under n holds v, as a filled-in field does.
func hasValue(n *render.AccessNode, v string) bool {
	return n != nil && (n.Value == v || slices.ContainsFunc(n.Children, func(k *render.AccessNode) bool { return hasValue(k, v) }))
}

// Opening the page within two minutes shows what was read at once, asking GitHub for neither the list nor the
// selected PR; later, it shows them while both are asked again in the background, as Refresh does.
func TestReviewsPageReopensFromCache(t *testing.T) {
	s, _, hub, _ := reviewScene(t)
	x := reviewsOf(s)
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	pass := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	x.SetClock(clock)
	const detail = "internal/ratelimit/bucket.go"
	s.Run("reviews.open", nil)
	s.Until("the detail", func() bool { return strings.Contains(s.Text(), detail) })
	asked := func(from int, what string) int {
		n := 0
		for _, c := range hub.Calls()[from:] {
			if strings.Contains(c, what) {
				n++
			}
		}
		return n
	}
	reopen := func(after time.Duration) int {
		x.ClosePage()
		s.Frame()
		pass(after)
		from := len(hub.Calls())
		s.Run("reviews.open", nil)
		s.Frame()
		for _, want := range []string{"Rate-limit the search endpoint", detail} {
			if !strings.Contains(s.Text(), want) {
				t.Errorf("reopened after %v, the page lacks %q at once:\n%s", after, want, s.Text())
			}
		}
		return from
	}
	from := reopen(90 * time.Second)
	s.Frames(5)
	if n := len(hub.Calls()) - from; n != 0 {
		t.Errorf("reopened after 90s, GitHub was asked again: %q", hub.Calls()[from:])
	}
	from = reopen(3 * time.Minute)
	s.Until("the list and PR asked again", func() bool { return asked(from, "/graphql") > 0 && asked(from, "/pulls/") > 0 })
	s.Frames(5)
	if !strings.Contains(s.Text(), detail) || strings.Contains(s.Text(), "for the rest") {
		t.Errorf("asked again, the detail went:\n%s", s.Text())
	}
	from = len(hub.Calls())
	s.Press("r")
	s.Until("Refresh asks for the list and PR", func() bool { return asked(from, "/graphql") > 0 && asked(from, "/pulls/") > 0 })
}

// A request's right-click menu starts its review as ↵ does, or in the background on the defaults: a tab behind the
// one in front, the page left open for the next.
func TestReviewsRowMenuStartsInBackground(t *testing.T) {
	s, lw, _, prs := reviewScene(t)
	x := reviewsOf(s)
	alias := filepath.Join(t.TempDir(), "acme", "api")
	if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lw.Repo, alias); err != nil {
		t.Fatal(err)
	}
	args := [][]string{{"remote", "add", "origin", alias}}
	for _, k := range []string{"ratelimit", "s3"} {
		args = append(args, []string{"update-ref", fmt.Sprintf("refs/pull/%d/head", prs[k].Number), "HEAD"})
	}
	for _, a := range args {
		if out, err := exec.Command("git", append([]string{"-C", lw.Repo}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	front := s.Store().State().Focus
	s.Run("reviews.open", nil)
	s.Until("the requests", func() bool { return s.Has("Rate-limit the search", "Retry flaky S3 uploads") })

	for _, k := range []string{"ratelimit", "s3"} {
		s.RightClick(s.TextPoint(prs[k].Title))
		s.Until("the row's menu", func() bool { return s.Has("Start review in background") })
		s.ClickText("Start review in background")
		s.UntilState("its review in a tab", func(st core.State) bool {
			se, ok := review.PRSession(s.Host(), prs[k].URL)
			return ok && slices.Contains(st.Tabs, se.ID())
		})
		if st := s.Store().State(); st.Focus != front || !x.PageOpen() {
			t.Fatalf("%s: the review came to the front (%v) or the page closed (open %v)", k, st.Focus != front, x.PageOpen())
		}
	}

	// A request with a session has it already: the menu says so.
	s.RightClick(s.TextPoint(prs["ratelimit"].Title))
	s.Until("the menu, Start review in background greyed", func() bool { return s.Node("Start review in background").Disabled })
	s.Type("Esc")

	// Start review is ↵'s: the launcher on Review PR.
	s.RightClick(s.TextPoint(prs["invoices"].Title))
	s.Until("the row's menu", func() bool { return s.Has("Start review in background") })
	s.ClickText("Start review")
	s.Until("the launcher", func() bool { return s.Find("New session") != nil })
}
