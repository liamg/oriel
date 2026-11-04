package ship_test

import (
	"fmt"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// The `pr` tab once the branch has a PR is its overview, read-only, named for it (`PR #790 · admin-ui`, the view's
// own title). Raising a PR from the tab lands on its overview. Edit PR (`ship.pr.open {project, edit: true}`) is the
// form, EDIT PR #790, with Cancel and Update PR: Cancel (`edit: false`) drops what was typed and Update goes back once
// the code host has it, both to the overview. The PR's link opens in the browser (`ship.browse`), and Copy link
// (`ship.copy-link`) puts it on the system clipboard.
func TestPRTabLandsOnTheOverview(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })

	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the Raise PR tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.WantFrame("Raise PR · search-api")
	s.ClickOn("Push + raise PR")
	shipSettle(s, t, "the overview", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return sh.PR != nil && strings.Contains(s.Text(), "Copy link")
	})
	prs := hub.PRs("acme/search-api")
	if len(prs) != 1 {
		t.Fatalf("the PRs on GitHub: %+v", prs)
	}
	pr := prs[0]
	num := fmt.Sprintf("PR #%d", pr.Number)
	s.WantFrame(num, num+" · search-api", "OPEN", "feat: x", pr.URL, "Copy link", "Edit PR", "CHECKS · NONE YET", "DESCRIPTION",
		"REVIEW", "BEFORE THIS SHIPS")
	if f := s.Text(); strings.Contains(f, "Update PR") || strings.Contains(f, "RAISE PR") {
		t.Errorf("the tab is still the form:\n%s", f)
	}
	if n := countRefs(s, kit.PRView); n != 1 {
		t.Errorf("%d PR tabs open", n)
	}
	s.Lint("the PR overview")

	s.ClickOn("Copy link")
	if s.Desktop.Text != pr.URL {
		t.Errorf("the clipboard has %q, not the PR's link", s.Desktop.Text)
	}
	s.ClickOn(pr.URL)
	if !slices.Contains(s.Desktop.Opened, pr.URL) {
		t.Errorf("the link opened %v", s.Desktop.Opened)
	}

	// Edit PR is the form; Cancel goes back with nothing sent.
	s.ClickOn("Edit PR")
	s.Frame()
	s.WantFrame(fmt.Sprintf("EDIT PR #%d", pr.Number), "Update PR", "Cancel")
	if v := s.Node("Title").Value; v != "feat: x" {
		t.Errorf("the title field has %q", v)
	}
	s.TypeInto("Title", " (typed)")
	s.ClickOn("Cancel")
	s.Frame()
	s.WantFrame("Copy link", "Edit PR")
	if strings.Contains(s.Text(), "Update PR") {
		t.Error("Cancel left the form up")
	}
	if got := hub.PRs("acme/search-api")[0].Title; got != "feat: x" {
		t.Errorf("Cancel changed the PR: %q", got)
	}

	// Again, and Update PR: back to the overview once GitHub has it.
	s.ClickOn("Edit PR")
	s.Frame()
	if v := s.Node("Title").Value; v != "feat: x" {
		t.Errorf("after Cancel the title field has %q", v)
	}
	s.TypeInto("Title", "!")
	s.ClickOn("Update PR")
	shipSettle(s, t, "the update", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return hub.PRs("acme/search-api")[0].Title == "feat: x!" && sh.PR != nil && sh.PR.Title == "feat: x!"
	})
	s.WantFrame("feat: x!", "Copy link", "Edit PR")
	if strings.Contains(s.Text(), "Update PR") {
		t.Error("Update PR left the form up")
	}

	// The Ship page's Edit PR opens the form; the dock's Open PR the overview.
	s.Run("ship.pr.open", registry.Args{"project": "search-api", "edit": true})
	s.Frame()
	s.WantFrame("Update PR", "Cancel")
	s.Run("ship.pr.open", registry.Args{"project": "search-api", "edit": false})
	s.Frame()
	s.WantFrame("Copy link")
	s.Run("project.panel", registry.Args{"name": "pull-request"})
	s.Frame()
	s.WantFrame("Open PR")
}

func countRefs(s *apptest.Scene, id string) int {
	n := 0
	for _, v := range s.Host().Layout().Visible() {
		if v.ID == id {
			n++
		}
	}
	return n
}

// The PR tab's overview: CHECKS, each with its state and time; DESCRIPTION, the PR's body as markdown; and REVIEW, the
// PR's review threads and conversation as they're mirrored into the session's threads (`HostThread`), each with its
// place, the reviewer and where they stand, its comments as markdown and Open (`request.open`).
func TestPROverviewSnapshot(t *testing.T) {
	s, se := shipSession(t)
	s.Run("ship.open", nil)
	s.SettleGit()
	s.Run("view.close", registry.Args{"id": kit.ShipView})
	x := shipOf(s)
	x.Fixture(se.ID, false, func(ships []ship.Read) []ship.Read {
		web := &ships[1]
		web.PR.URL = "https://github.com/acme/web-dashboard/pull/812"
		web.PR.Reviewers = []string{"mchen"}
		web.PR.Body = "## What\n\nThe search page backs off when the API answers 429, for as long as `Retry-After` says.\n\n" +
			"## Why\n\nENG-2231: the retries were hammering the API.\n\n- unit tests for the backoff\n- e2e on chromium\n"
		return ships
	})
	said := s.Now.Add(-2 * time.Hour)
	se.Threads = append(se.Threads,
		core.Thread{ID: "gh1", Origin: core.OriginGitHub, Status: core.ThreadOpen,
			Anchor: &core.Anchor{Project: "web-dashboard", File: "src/search/backoff.ts", Line: 42},
			Hosted: &core.HostThread{Project: "web-dashboard", Repo: "acme/web-dashboard", PR: 812, Root: 9001, Reviewer: "mchen",
				State: gh.ReviewChangesRequested},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorHost, Name: "mchen", Time: said,
				Body: "Cap the wait: a `Retry-After` of an hour shouldn't freeze the page."}}},
		core.Thread{ID: "gh2", Origin: core.OriginGitHub, Status: core.ThreadOpen,
			Hosted: &core.HostThread{Project: "web-dashboard", Repo: "acme/web-dashboard", PR: 812},
			Comments: []core.Comment{{ID: "c2", Author: core.AuthorHost, Name: "jkowalski", Time: said.Add(30 * time.Minute),
				Body: "Looks good once the cap's in."}}},
		core.Thread{ID: "other", Origin: core.OriginGitHub, Status: core.ThreadOpen,
			Hosted:   &core.HostThread{Project: "search-api", Repo: "acme/search-api", PR: 811},
			Comments: []core.Comment{{ID: "c3", Author: core.AuthorHost, Name: "mchen", Body: "Not this PR's."}}})
	if err := s.Store().Apply(core.SessionSynced{Session: se}); err != nil {
		t.Fatal(err)
	}
	s.Run("ship.pr.open", registry.Args{"project": "web-dashboard"})
	gatesCounted(s, t)
	s.Golden("ship-pr-overview")
	s.WantFrame("PR #812 · web-dashboard", "PR #812", "DRAFT", "rethink/search-endpoint → main · 3 commits",
		"feat(search): back off on 429 using Retry-After", "https://github.com/acme/web-dashboard/pull/812", "Copy link", "Ready for review",
		"Edit PR", "CHECKS · 5 OF 7", "running 3m", "queued", "DESCRIPTION", "What", "Retry-After", "REVIEW · 2 THREADS",
		"src/search/backoff.ts:42", "@MCHEN", "CHANGES REQUESTED", "Cap the wait", "conversation", "jkowalski", "Looks good once the cap's in.",
		"BEFORE THIS SHIPS")
	if strings.Contains(s.Text(), "Not this PR's.") {
		t.Error("another PR's thread is on the overview")
	}
	s.Lint("the PR overview")

	s.Click(apptest.Centre(s.Within("Thread src/search/backoff.ts:42", "Open").Bounds), 0)
	s.Frame()
	if !slices.ContainsFunc(s.Host().Layout().Visible(), func(v layout.ViewRef) bool { return v.ID == kit.RequestView }) {
		t.Errorf("Open didn't open the request: %+v", s.Host().Layout().Visible())
	}
}

// With no agent draft, Raise PR starts from the branch's first commit, pushed or not: its subject is the title and
// its body sits above the template.
func TestPRFormStartsFromThePushedCommit(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x\n\nAdds x."})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	s.Run("ship.push", registry.Args{"project": "search-api"})
	shipSettle(s, t, "the push", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Pushed && len(sh.Unpushed) == 0 })

	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the Raise PR tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.ClickOn("Push + raise PR")
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil })
	if got := hub.PRs("acme/search-api"); len(got) != 1 || got[0].Title != "feat: x" || got[0].Body != "Adds x.\n\n## What\n\n## Why\n" {
		t.Errorf("raised from the pushed commit's message and the template: %+v", got)
	}
}
