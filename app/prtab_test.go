package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// ui.md 18: "The `pr` tab once the branch has a PR is its **overview**, read-only, named for it (`PR #790 · admin-ui`,
// the view's own title)" … "Raising a PR from the tab lands on its overview." … "**Edit PR** (`ship.pr.open {project,
// edit: true}`) is the form, EDIT PR #790, with **Cancel** and **Update PR**: Cancel (`edit: false`) drops what was
// typed and Update goes back once the code host has it, both to the overview." … "the PR's link (it opens in the
// browser, `ship.browse`) and **Copy link** (`ship.copy-link`: the system clipboard)".
func TestPRTabLandsOnTheOverview(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := s.ship()
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the file", func() bool { sh, _ := x.ship(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.Commits == 1 })

	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.until(t, "the Raise PR tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.wantFrame(t, "Raise PR · search-api")
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.shipSettle(t, "the overview", func() bool {
		sh, _ := x.ship(se.ID, "search-api")
		return sh.PR != nil && strings.Contains(s.frameText(), "Copy link")
	})
	prs := hub.PRs("acme/search-api")
	if len(prs) != 1 {
		t.Fatalf("the PRs on GitHub: %+v", prs)
	}
	pr := prs[0]
	num := fmt.Sprintf("PR #%d", pr.Number)
	s.wantFrame(t, num, num+" · search-api", "OPEN", "feat: x", pr.URL, "Copy link", "Edit PR", "CHECKS · NONE YET", "DESCRIPTION",
		"REVIEW", "BEFORE THIS SHIPS")
	if f := s.frameText(); strings.Contains(f, "Update PR") || strings.Contains(f, "RAISE PR") {
		t.Errorf("the tab is still the form:\n%s", f)
	}
	if n := countRefs(s, PRView); n != 1 {
		t.Errorf("%d PR tabs open", n)
	}
	s.lint("the PR overview")

	s.click(centre(s.access("Copy link").Bounds))
	if s.clip.text != pr.URL {
		t.Errorf("the clipboard has %q, not the PR's link", s.clip.text)
	}
	s.click(centre(s.access(pr.URL).Bounds))
	if !slices.Contains(s.clip.opened, pr.URL) {
		t.Errorf("the link opened %v", s.clip.opened)
	}

	// Edit PR is the form; Cancel goes back with nothing sent.
	s.click(centre(s.access("Edit PR").Bounds))
	s.frame()
	s.wantFrame(t, fmt.Sprintf("EDIT PR #%d", pr.Number), "Update PR", "Cancel")
	if v := s.access("Title").Value; v != "feat: x" {
		t.Errorf("the title field has %q", v)
	}
	s.typeInto(t, "Title", " (typed)")
	s.click(centre(s.access("Cancel").Bounds))
	s.frame()
	s.wantFrame(t, "Copy link", "Edit PR")
	if strings.Contains(s.frameText(), "Update PR") {
		t.Error("Cancel left the form up")
	}
	if got := hub.PRs("acme/search-api")[0].Title; got != "feat: x" {
		t.Errorf("Cancel changed the PR: %q", got)
	}

	// Again, and Update PR: back to the overview once GitHub has it.
	s.click(centre(s.access("Edit PR").Bounds))
	s.frame()
	if v := s.access("Title").Value; v != "feat: x" {
		t.Errorf("after Cancel the title field has %q", v)
	}
	s.typeInto(t, "Title", "!")
	s.click(centre(s.access("Update PR").Bounds))
	s.shipSettle(t, "the update", func() bool {
		sh, _ := x.ship(se.ID, "search-api")
		return hub.PRs("acme/search-api")[0].Title == "feat: x!" && sh.PR != nil && sh.PR.Title == "feat: x!"
	})
	s.wantFrame(t, "feat: x!", "Copy link", "Edit PR")
	if strings.Contains(s.frameText(), "Update PR") {
		t.Error("Update PR left the form up")
	}

	// The Ship page's Edit PR opens the form; the dock's Open PR the overview.
	s.run("ship.pr.open", registry.Args{"project": "search-api", "edit": true})
	s.frame()
	s.wantFrame(t, "Update PR", "Cancel")
	s.run("ship.pr.open", registry.Args{"project": "search-api", "edit": false})
	s.frame()
	s.wantFrame(t, "Copy link")
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.frame()
	s.wantFrame(t, "Open PR")
}

func countRefs(s *errScene, id string) int {
	n := 0
	for _, v := range s.visibleRefs() {
		if v.ID == id {
			n++
		}
	}
	return n
}

// ui.md 18, the PR tab's overview: "CHECKS, each with its state and time" … "DESCRIPTION, the PR's body as markdown; and
// REVIEW, the PR's review threads and conversation as they're mirrored into the session's threads (`HostThread`), each
// with its place, the reviewer and where they stand, its comments as markdown and **Open** (`request.open`)".
func TestPROverviewSnapshot(t *testing.T) {
	s, se := mockup18(t)
	s.run("ship.open", nil)
	s.settleGit(t)
	s.run("view.close", registry.Args{"id": ShipView})
	x := s.ship()
	web := &x.reports[se.ID].ships[1]
	web.PR.URL = "https://github.com/acme/web-dashboard/pull/812"
	web.PR.Reviewers = []string{"mchen"}
	web.PR.Body = "## What\n\nThe search page backs off when the API answers 429, for as long as `Retry-After` says.\n\n" +
		"## Why\n\nENG-2231: the retries were hammering the API.\n\n- unit tests for the backoff\n- e2e on chromium\n"
	said := s.now.Add(-2 * time.Hour)
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
	if err := s.store.Apply(core.SessionSynced{Session: se}); err != nil {
		t.Fatal(err)
	}
	s.run("ship.pr.open", registry.Args{"project": "web-dashboard"})
	s.golden("ship-pr-overview")
	s.wantFrame(t, "PR #812 · web-dashboard", "PR #812", "DRAFT", "rethink/search-endpoint → main · 3 commits",
		"feat(search): back off on 429 using Retry-After", "https://github.com/acme/web-dashboard/pull/812", "Copy link", "Ready for review",
		"Edit PR", "CHECKS · 5 OF 7", "running 3m 00s", "queued", "DESCRIPTION", "What", "Retry-After", "REVIEW · 2 THREADS",
		"src/search/backoff.ts:42", "@MCHEN", "CHANGES REQUESTED", "Cap the wait", "conversation", "jkowalski", "Looks good once the cap's in.",
		"BEFORE THIS SHIPS")
	if strings.Contains(s.frameText(), "Not this PR's.") {
		t.Error("another PR's thread is on the overview")
	}
	s.lint("the PR overview")

	s.click(centre(s.within("Thread src/search/backoff.ts:42", "Open").Bounds))
	s.frame()
	if !slices.ContainsFunc(s.visibleRefs(), func(v layout.ViewRef) bool { return v.ID == RequestView }) {
		t.Errorf("Open didn't open the request: %+v", s.visibleRefs())
	}
}
