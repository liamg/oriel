package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/registry"
)

// prReviewScene is the Reviews page's scene with the ratelimit PR's head pushed to a repo whose origin has it, and a
// review of it started with the fake agent: in front, its window opened on it, or in the background. push pushes
// bucket.go's next version to the PR and answers its head.
func prReviewScene(t *testing.T, background bool) (s *errScene, lw *localHost, hub *ghtest.Server, pr gh.PR, id core.SessionID, push func(string) string) {
	s, lw, hub, prs := reviewScene(t)
	pr = prs["ratelimit"]
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", lw.repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	push = func(body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(lw.repo, "bucket.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("checkout", "-q", "-B", "mk/search-ratelimit")
		git("add", "bucket.go")
		git("-c", "user.name=mkhan", "-c", "user.email=mk@acme.dev", "commit", "-q", "-m", "bucket")
		head := git("rev-parse", "HEAD")
		git("checkout", "-q", "-")
		git("update-ref", fmt.Sprintf("refs/pull/%d/head", pr.Number), head)
		hub.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "mkhan", HeadSHA: head, Additions: 3,
			Files: []gh.PRFile{{Path: "bucket.go", Additions: 3}}})
		return head
	}
	git("remote", "add", "origin", lw.repo)
	push("package ratelimit\n\nfunc Take() int { return 1 }\n")
	hub.DiffLines("acme/api", pr.Number, "bucket.go:1", "bucket.go:2", "bucket.go:3")

	done := make(chan error, 1)
	go func() {
		var err error
		id, err = s.sh.sessions.start(context.Background(), registry.Args{"kind": string(core.KindReview), "pr": pr.URL,
			"repos": []string{lw.repo}, "provider": "fake", "background": background})
		done <- err
	}()
	s.until(t, "the review session", func() bool {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return true
		default:
			return false
		}
	})
	if !background {
		s.untilState(t, "the review in front", func(st core.State) bool { return st.Focus == id })
		s.until(t, "the window on the review", s.reopen)
	}
	return s, lw, hub, pr, id, push
}

// A review session is a request in Requests, and has its own entry in the rail in place of Raise PR's checklist; it
// opens on the Explorer's Changed and its tab, where the review's comments and the user's own are triaged (with keys,
// or on their cards in the diff), reworded by hand or by the agent, summed up by the agent, previewed and posted with
// severity badges. The posted comments come back as GitHub threads; resolving one resolves it on GitHub; the author's
// reply is a notice; a push brings a re-review; a merged PR says so; and gc in a review is a comment for the author.
func TestReviewSessionPRTab(t *testing.T) {
	s, lw, hub, pr, id, push := prReviewScene(t, false)
	sess, _ := s.store.State().Session(id)
	se := sess.Snapshot()
	ref := fmt.Sprintf("api#%d", pr.Number)
	if se.Name != ref || len(se.Threads) != 1 || se.Threads[0].Title != "Review "+ref ||
		!strings.Contains(se.Threads[0].Comments[0].Body, "mk/search-ratelimit into main") {
		t.Fatalf("the review's name %q and request %+v", se.Name, se.Threads)
	}
	for _, e := range registry.RailEntries(s.reg) {
		if (e.Name == "ship" || e.Name == "git" || e.Name == "pull-request") && (e.Shown == nil || e.Shown("")) {
			t.Errorf("%s is offered in a review", e.Name)
		}
		if e.Name == "review" && (e.Shown == nil || !e.Shown("")) {
			t.Error("the review's own entry isn't in the rail")
		}
	}

	a := lw.fake.WaitStarted(t)
	for a.Spec.Pack.Name != "review" {
		a = lw.fake.WaitStarted(t) // the AI review's reviewer starts first
	}
	a.Call(t, "comment", map[string]any{"file": "bucket.go", "line": 3, "severity": "high", "body": "Takes one whatever the cost."})
	a.Call(t, "comment", map[string]any{"file": "bucket.go", "line": 1, "severity": "nit", "body": "Name the package for what it limits."})
	s.run("request.comment", registry.Args{"file": "bucket.go", "line": 2, "body": "Say why it's 1.", "outgoing": true})
	review := func() registry.RailEntry {
		for _, e := range registry.RailEntries(s.reg) {
			if e.Name == "review" {
				return e
			}
		}
		return registry.RailEntry{}
	}
	s.until(t, "the review's rail entry lit, with 3 comments to triage", func() bool { return review().Active("") && review().Badge() == 3 })
	s.until(t, "the review tab and the Explorer on its changes, opened", func() bool {
		text := s.frameText()
		return strings.Contains(text, "YOUR REVIEW") && strings.Contains(text, "Takes one whatever") && strings.Contains(text, "Adds a token bucket") &&
			strings.Contains(text, "CHANGED 1") && strings.Contains(text, "vs origin/m") && strings.Contains(text, "3 comments · 0 of 3 triaged · 2 to post")
	})
	thread := func(st core.State, body string) core.Thread {
		sess, _ := st.Session(id)
		for _, th := range sess.Snapshot().Threads {
			if len(th.Comments) > 0 && th.Comments[0].Body == body {
				return th
			}
		}
		return core.Thread{}
	}
	pick := func(body string) core.ReviewPick {
		st := s.store.State()
		sess, _ := st.Session(id)
		if r := sess.Snapshot().Review; r != nil {
			return r.Picks[string(thread(st, body).ID)]
		}
		return core.ReviewPick{}
	}
	if th := thread(s.store.State(), "Say why it's 1."); !th.Outgoing || th.WaitsOnAgent() {
		t.Fatalf("the user's comment for the author went to the agent: %+v", th)
	}

	// Triage with keys: a click selects a row, 2 rates it, y keeps it, e edits its words; a second click shows it.
	s.click(centre(s.access("Show bucket.go:1").Bounds))
	s.wantFrame(t, "j k", "keep", "drop", "severity", "reword", "show in diff")
	if m := s.sh.keyModes()[0]; m != PRReviewKeyMode {
		t.Fatalf("the selected comment takes keys in %q", m)
	}
	s.keyCommand(t, "2")
	s.keyCommand(t, "y") // and on to the next undecided
	s.untilState(t, "the nit kept and rated high, in the session", func(core.State) bool {
		p := pick("Name the package for what it limits.")
		return p.State == core.PickKeep && p.Severity == "high"
	})
	s.click(centre(s.access("Show bucket.go:1").Bounds))
	s.keyCommand(t, "e")
	s.until(t, "its words open to edit", func() bool { return strings.Contains(s.frameText(), "The reviewer's words") })
	s.typeInto(t, "What the author gets", " Ratelimit, say.")
	s.clickText(t, "Save")
	s.untilState(t, "the words the author gets", func(core.State) bool {
		return pick("Name the package for what it limits.").Body == "Name the package for what it limits. Ratelimit, say."
	})
	s.click(centre(s.access("Show bucket.go:1").Bounds))
	s.until(t, "the comment's line in the diff", func() bool { return strings.Contains(s.frameText(), "hunks reviewed") })

	// In the diff, each comment's card says where it stands in the review, and drops (or keeps) it there.
	s.wantFrame(t, "YOUR REVIEW", "In the review ↗")
	s.clickText(t, "Drop")
	var dropped core.Thread
	s.untilState(t, "a comment dropped from its card, its thread resolved", func(st core.State) bool {
		sess, _ := st.Session(id)
		se := sess.Snapshot()
		for k, p := range se.Review.Picks {
			if th, ok := se.Thread(core.ThreadID(k)); ok && p.State == core.PickDrop && th.Status == core.ThreadResolved {
				dropped = th
				return true
			}
		}
		return false
	})
	s.run("review.pr.pick", registry.Args{"item": string(dropped.ID), "state": core.PickKeep})
	s.untilState(t, "kept again, its thread open", func(st core.State) bool {
		th := thread(st, dropped.Comments[0].Body)
		return th.Open() && pick(dropped.Comments[0].Body).State == core.PickKeep
	})

	// The rail's Review entry brings the tab back.
	if review().Active("") {
		t.Fatal("the review's entry is lit with the diff in front")
	}
	s.click(centre(s.access("Review").Bounds))
	s.until(t, "the review tab, from the rail", func() bool { return review().Active("") && strings.Contains(s.frameText(), "YOUR REVIEW") })

	// The agent rewords the high one, and its rewording is used.
	high := thread(s.store.State(), "Takes one whatever the cost.")
	s.run("review.pr.rework", registry.Args{"item": string(high.ID), "how": "friendlier"})
	s.untilState(t, "the agent asked to reword it, in its thread", func(st core.State) bool {
		c, ok := thread(st, "Takes one whatever the cost.").Last()
		return ok && c.Author == core.AuthorYou && strings.Contains(c.Body, "rework request") && strings.Contains(c.Body, "> Takes one whatever the cost.")
	})
	a.Call(t, "reply", map[string]any{"thread": string(high.ID), "body": "Could Take charge the request's cost rather than 1?"})
	s.until(t, "the rewording", func() bool { return strings.Contains(s.frameText(), "THE AGENT'S REWORDING") })
	s.clickText(t, "Use it")
	s.untilState(t, "the rewording used", func(core.State) bool {
		return pick("Takes one whatever the cost.").Body == "Could Take charge the request's cost rather than 1?"
	})

	// The agent drafts the summary into the box.
	s.clickText(t, "Draft summary")
	var ask core.Thread
	s.untilState(t, "the summary asked for", func(st core.State) bool {
		sess, _ := st.Session(id)
		for _, th := range sess.Snapshot().Threads {
			if th.Title == "Draft the review summary" {
				ask = th
				return true
			}
		}
		return false
	})
	a.Call(t, "reply", map[string]any{"thread": string(ask.ID), "body": "the cost isn't charged yet; the rest is optional.", "resolve": true})
	s.until(t, "the summary in the box", func() bool { return strings.Contains(s.frameText(), "the cost isn't charged yet") })

	// The agent's done; Request changes, the suggested verdict, previews the review, and Post sends it.
	a.Call(t, "reply", map[string]any{"thread": string(se.Threads[0].ID), "body": "Looks fine bar the cost.", "resolve": true})
	s.until(t, "the review done", func() bool {
		for _, n := range s.store.State().Notifications {
			if n.Title == "Review done" && strings.Contains(n.Text, "3 comments to triage") {
				return true
			}
		}
		return false
	})
	s.run("notice.dismiss", registry.Args{"id": "review.pr.done." + string(id)})
	s.clickText(t, "Request changes…")
	s.until(t, "the preview", func() bool { return strings.Contains(s.frameText(), "Post: Changes requested") })
	badge := "![high](https://img.shields.io/badge/severity-high-EF6C00)\n\n"
	s.wantFrame(t, "Could Take charge the request's cost rather than 1?", "Say why it's 1.")
	s.clickText(t, "Post: Changes requested")
	s.until(t, "the review on GitHub", func() bool { return len(hub.Reviews("acme/api", pr.Number)) == 1 })
	rs, cs := hub.Reviews("acme/api", pr.Number), hub.ReviewComments("acme/api", pr.Number)
	bodies := map[string]bool{}
	for _, c := range cs {
		bodies[c.Body] = true
	}
	if rs[0].State != gh.ReviewChangesRequested || rs[0].Body != "the cost isn't charged yet; the rest is optional." || len(cs) != 3 ||
		!bodies[badge+"Could Take charge the request's cost rather than 1?"] || !bodies[badge+"Name the package for what it limits. Ratelimit, say."] ||
		!bodies["Say why it's 1."] {
		t.Fatalf("posted %+v with %+v", rs, cs)
	}

	// The posted comments come back as GitHub threads, and resolving one in Oriel resolves it there.
	s.untilState(t, "the posted comments linked", func(st core.State) bool {
		th := thread(st, "Takes one whatever the cost.")
		return th.Hosted != nil && th.Hosted.Root != 0
	})
	s.until(t, "the sent card", func() bool {
		return strings.Contains(s.frameText(), "SENT  3") && strings.Contains(s.frameText(), "WAITING ON AUTHOR")
	})
	high = thread(s.store.State(), "Takes one whatever the cost.")
	s.run("request.resolve", registry.Args{"id": string(id), "thread": string(high.ID)})
	s.until(t, "resolved on GitHub", func() bool { return hub.Resolved(high.Hosted.Root) })

	// The author's reply on GitHub is a notice.
	nit := thread(s.store.State(), "Name the package for what it limits.")
	hub.AddReviewComment("acme/api", pr.Number, gh.ReviewComment{User: "mkhan", InReplyTo: nit.Hosted.Root, Body: "Renamed it to ratelimit."})
	s.run("github.sync", registry.Args{"id": string(id)})
	s.until(t, "the author's reply, told", func() bool {
		for _, n := range s.store.State().Notifications {
			if n.Title == "mkhan replied" && strings.Contains(n.Text, "Renamed it to ratelimit.") {
				return true
			}
		}
		return false
	})

	// A push is seen by the next sync: the checkout moves to it, and the agent's asked to review it.
	head := push("package ratelimit\n\nfunc Take(cost int) int { return cost }\n")
	s.run("github.sync", registry.Args{"id": string(id)})
	s.untilState(t, "the re-review asked", func(st core.State) bool {
		sess, _ := st.Session(id)
		se := sess.Snapshot()
		if p, _ := prProject(se); p.Commit != head {
			return false
		}
		for _, th := range se.Threads {
			if th.Title == "Review the new commits" && strings.Contains(th.Comments[0].Body, "waiting on author") {
				return true
			}
		}
		return false
	})

	// Once the PR is merged, the tab says so and offers to archive the review; nothing more can be posted.
	hub.Merge("acme/api", pr.Number)
	s.run("github.sync", registry.Args{"id": string(id)})
	s.run("ship.pr.open", nil)
	s.until(t, "the merged PR", func() bool {
		text := s.frameText()
		return strings.Contains(text, "This pull request was merged") && strings.Contains(text, "It's no longer open")
	})

	// gc on a line of a review is a comment for the author, and asking the agent is a click (or ga) away.
	sess, _ = s.store.State().Session(id)
	file := filepath.Join(sess.Snapshot().Projects[0].Dir, "bucket.go")
	s.run("editor.open", registry.Args{"path": file})
	s.run("thread.new", registry.Args{"file": file, "line": 2})
	s.until(t, "the composer, for the author", func() bool { return strings.Contains(s.frameText(), "Comment for the author") })
	s.clickText(t, "Ask the agent")
	s.until(t, "the composer, asking the agent", func() bool { return strings.Contains(s.frameText(), "New thread") })
}

func (s *errScene) prReview() *prReviewPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*prReviewPlugin); ok {
			return x
		}
	}
	return nil
}

// A review the assistant started behind the one in front opens on its Explorer and tab the first time it's brought
// to the front.
func TestReviewStartedBehindOpensWhenShown(t *testing.T) {
	s, _, _, _, id, _ := prReviewScene(t, true)
	if s.store.State().Focus == id {
		t.Fatal("a review started in the background came to the front")
	}
	s.run("session.open", registry.Args{"id": string(id)})
	s.until(t, "the window on the review", s.reopen)
	s.until(t, "its Explorer and tab", func() bool {
		text := s.frameText()
		return strings.Contains(text, "YOUR REVIEW") && strings.Contains(text, "CHANGED 1")
	})
}
