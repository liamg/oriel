package mirror_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/mirror"
	"github.com/liamg/oriel/core"
	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/integ/github/ghtest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func mirrorOf(s *apptest.Scene) *mirror.Mirror { return apptest.PluginOf[*mirror.Mirror](s, "mirror") }

// The user's forward posts the agent's draft. A code host's threads (GitHub's here) are mirrored inline, the agent
// drafts a reply, and you forward it: "Review · L80 · @priya-k · changes requested · synced 30s ago", the reviewer's
// comment, "Claude draft · not sent", "↗ Forward to GitHub", "Edit", "Claude's draft reaches GitHub when you forward
// it.", and the status line's "PR 1 changes requested".
func TestGitHubReviewForward(t *testing.T) {
	s, lw, hub, se, _ := apptest.ShipScene(t)
	x := mirrorOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := apptest.ProjectDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	rig.WriteFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Silently defaulting to 2 will hide config mistakes. Can this return an error instead?"})
	hub.AddReview("acme/search-api", pr.Number, gh.Review{User: "priya-k", Association: "MEMBER", State: gh.ReviewChangesRequested})

	s.Run("review.threads.sync", nil)
	var tid core.ThreadID
	s.Until("the mirrored thread", func() bool {
		x.Wait()
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Origin == core.OriginCodeHost && th.Hosted.Host == "github" && th.Hosted.Key == strconv.FormatInt(root.ID, 10) {
				tid = th.ID
			}
		}
		return tid != ""
	})
	if batch := ag.WaitSent(t).Text; !strings.Contains(batch, "Can this return an error instead?") || !strings.Contains(batch, `origin="github"`) {
		t.Fatalf("the agent was given %q", batch)
	}
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Agreed. `Handle` now returns an error."}); !strings.Contains(out, "drafted") {
		t.Errorf("reply: %q", out)
	}
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})

	s.Run("diff.open", registry.Args{"path": file})
	s.Until("the review block between the diff's lines", func() bool {
		f := s.Text()
		return strings.Contains(f, "Review") && strings.Contains(f, "↗ Forward to GitHub")
	})
	s.WantFrame("Review", "· L3 ·", "@PRIYA-K", "CHANGES REQUESTED", "synced", "priya-k", "Can this return an error instead?",
		"draft · not sent", "Edit", "Fake's draft reaches GitHub when you forward it.", "PR 1 changes requested")
	s.Lint("a GitHub thread in the diff")
	if o := s.UI().Overflowing(); len(o) > 0 {
		t.Errorf("text drawn past its box:\n%s", strings.Join(o, "\n"))
	}
	if p := ghPosts(hub); len(p) != 0 {
		t.Fatalf("GitHub was written to before the forward: %q", p)
	}

	// Edit, then save: the draft changes, still unsent.
	s.ClickOn("Edit")
	s.Until("the draft editor", func() bool { return s.Count("Edit the draft reply") > 0 })
	s.TypeInto("Edit the draft reply", " Logged too.")
	s.ClickOn("Save draft")
	s.Until("the edited draft", func() bool {
		return strings.Contains(s.Text(), "Logged too.") && s.Count("Edit the draft reply") == 0
	})
	if d := s.Draft(se.ID, tid); d.Body != "Agreed. `Handle` now returns an error. Logged too." || !d.Draft {
		t.Errorf("the edited draft %+v", d)
	}

	// Forward: posted once, as the user, and shown as sent.
	s.ClickOn("↗ Forward to GitHub")
	s.Until("the forward", func() bool {
		x.Wait()
		return strings.Contains(s.Text(), "sent to github")
	})
	if p := ghPosts(hub); len(p) != 1 || !strings.HasSuffix(p[0], "/replies") {
		t.Fatalf("posts %q, want one reply", p)
	}
	cs := hub.ReviewComments("acme/search-api", pr.Number)
	if last := cs[len(cs)-1]; last.Body != "Agreed. `Handle` now returns an error. Logged too." || last.User != "you" {
		t.Errorf("on GitHub: %+v", last)
	}
	if strings.Contains(s.Text(), "↗ Forward to GitHub") {
		t.Error("a sent reply still offers Forward")
	}
	if err := s.Host().Run(context.Background(), "review.draft.forward", registry.Args{"thread": string(tid)}); err == nil {
		t.Error("forwarding again went through")
	}

	// The echo doesn't come back as another comment.
	s.Run("review.threads.sync", nil)
	x.Wait()
	st := s.Store().State()
	sess, _ := st.Session(se.ID)
	th, _ := sess.Snapshot().Thread(tid)
	if len(th.Comments) != 2 {
		t.Errorf("after syncing the echo: %+v", th.Comments)
	}
	if p := ghPosts(hub); len(p) != 1 {
		t.Errorf("posts %q", p)
	}
}

func ghPosts(hub interface{ Calls() []string }) []string {
	var out []string
	for _, c := range hub.Calls() {
		if strings.HasPrefix(c, "POST /repos/") || strings.HasPrefix(c, "PATCH /repos/") {
			out = append(out, c)
		}
	}
	return out
}

// A mirrored comment shows its reactions there, the user's own lit. A click on one adds the user's or takes it back;
// 👍 is offered on a comment with none yet, and the picker offers the host's every reaction.
func TestReactToAMirroredComment(t *testing.T) {
	s, lw, hub, se, _ := apptest.ShipScene(t)
	x := mirrorOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := apptest.ProjectDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	rig.WriteFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Can this return an error instead?"})
	reply := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "jkowalski", Association: "MEMBER", InReplyTo: root.ID, Body: "Seconded."})
	hub.ReactAs("bo", ghtest.LineComment, root.ID, "hooray")

	s.Run("review.threads.sync", nil)
	x.Wait()
	s.Run("diff.open", registry.Args{"path": file})
	s.Until("the reactions under the comments", func() bool {
		return s.Count("🎉 1 on priya-k's comment") > 0 && s.Count("React 👍 to jkowalski's comment") > 0
	})
	s.Lint("a mirrored comment's reactions")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.Node("🎉 1 on priya-k's comment").Bounds)})
	s.Frame()
	s.SetNow(s.Now().Add(time.Second))
	s.Frames(2)
	if !strings.Contains(s.Text(), "bo · React with 🎉") {
		t.Errorf("hovering 🎉 doesn't name who reacted:\n%s", s.Text())
	}
	s.PointerLeaves()

	mine := func(id int64) map[string]bool {
		out := map[string]bool{}
		for name, who := range hub.ReactionsOn(ghtest.LineComment, id) {
			out[name] = slices.Contains(who, "you")
		}
		return out
	}
	s.ClickOn("🎉 1 on priya-k's comment")
	s.Until("the user's 🎉 shown", func() bool { x.Wait(); return s.Count("🎉 2 on priya-k's comment") > 0 })
	if !mine(root.ID)["hooray"] {
		t.Errorf("GitHub's reactions after a click on 🎉 1: %v", hub.ReactionsOn(ghtest.LineComment, root.ID))
	}
	s.ClickOn("🎉 2 on priya-k's comment")
	s.Until("the user's 🎉 taken back", func() bool { x.Wait(); return s.Count("🎉 1 on priya-k's comment") > 0 })
	if mine(root.ID)["hooray"] {
		t.Error("the user's 🎉 is still on GitHub")
	}

	s.ClickOn("React 👍 to jkowalski's comment")
	s.Until("the reply's 👍", func() bool { x.Wait(); return s.Count("👍 1 on jkowalski's comment") > 0 })
	if !mine(reply.ID)["+1"] || mine(root.ID)["+1"] {
		t.Errorf("👍 went on the wrong comment: first %v, reply %v", mine(root.ID), mine(reply.ID))
	}

	s.ClickOn("Add a reaction to priya-k's comment")
	s.Until("the picker", func() bool { return s.Count("🚀") > 0 })
	s.ClickOn("🚀")
	s.Until("the user's 🚀", func() bool { x.Wait(); return s.Count("🚀 1 on priya-k's comment") > 0 })
	if !mine(root.ID)["rocket"] {
		t.Errorf("GitHub's reactions after picking 🚀: %v", hub.ReactionsOn(ghtest.LineComment, root.ID))
	}
}

// A click on a reaction while the last click's is still being sent changes the user's mind rather than being turned
// away: what's shown follows the clicks, and GitHub ends with the last.
func TestReactAgainWhileSending(t *testing.T) {
	s, lw, hub, se, _ := apptest.ShipScene(t)
	x := mirrorOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := apptest.ProjectDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	rig.WriteFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Can this return an error instead?"})
	hub.ReactAs("you", ghtest.LineComment, root.ID, "hooray")

	s.Run("review.threads.sync", nil)
	x.Wait()
	s.Run("diff.open", registry.Args{"path": file})
	s.Until("the user's 🎉", func() bool { return s.Count("🎉 1 on priya-k's comment") > 0 })

	release := hub.HoldReactions(t)
	s.ClickOn("🎉 1 on priya-k's comment")
	s.Until("the 🎉 shown taken back, still there to click", func() bool { return s.Count("🎉 0 on priya-k's comment") > 0 })
	s.Midway(func() { // the first click's reaction is still being sent
		s.ClickOn("🎉 0 on priya-k's comment")
		s.Until("the 🎉 shown again", func() bool { return s.Count("🎉 1 on priya-k's comment") > 0 })
		if strings.Contains(s.Text(), "being sent") {
			t.Errorf("the second click was turned away:\n%s", s.Text())
		}
	})
	release()
	x.Wait()
	if who := hub.ReactionsOn(ghtest.LineComment, root.ID)["hooray"]; !slices.Equal(who, []string{"you"}) {
		t.Errorf("GitHub's 🎉 after taking it back and reacting again: %v", who)
	}
	s.Until("the 🎉 as GitHub has it", func() bool { return s.Count("🎉 1 on priya-k's comment") > 0 })
}

// A sync that read GitHub before the user's reaction went out, and ends after, doesn't undo the reaction on screen:
// it stays as sent until a sync that started after it.
func TestReactWhileASyncRuns(t *testing.T) {
	s, lw, hub, se, _ := apptest.ShipScene(t)
	x := mirrorOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := apptest.ProjectDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	rig.WriteFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Can this return an error instead?"})
	hub.ReactAs("bo", ghtest.LineComment, root.ID, "hooray")

	s.Run("review.threads.sync", nil)
	x.Wait()
	s.Run("diff.open", registry.Args{"path": file})
	s.Until("bo's 🎉", func() bool { return s.Count("🎉 1 on priya-k's comment") > 0 })

	held := hub.HoldReactionReads(t)
	take := func(what string) func() {
		var release func()
		s.Until(what, func() bool {
			r, ok := held()
			release = r
			return ok
		})
		return release
	}
	stale := false
	sawStale := func(int) { stale = stale || s.Count("🎉 1 on priya-k's comment") > 0 }
	s.Midway(func() {
		s.Run("review.threads.sync", nil)
		old := take("a sync's read of the reactions, before the user's")
		s.ClickOn("🎉 1 on priya-k's comment")
		s.Until("GitHub has the user's 🎉", func() bool { return slices.Contains(hub.ReactionsOn(ghtest.LineComment, root.ID)["hooray"], "you") })
		old()
		next := take("the read of the sync after the reaction")
		s.UntilEach("the user's 🎉 shown", sawStale, func() bool { return stale || s.Count("🎉 2 on priya-k's comment") > 0 })
		next()
	})
	x.Wait()
	s.UntilEach("the 🎉 as GitHub has it", sawStale, func() bool { return s.Count("🎉 2 on priya-k's comment") > 0 })
	if stale {
		t.Error("the sync that read GitHub before the reaction showed the 🎉 taken back")
	}
}

// The agent's draft reply on a review thread that offers a fix has Do it beside ↗ Forward: a click is your "Do it",
// to the agent, and nothing reaches GitHub.
func TestDoItOnAReviewThreadsDraft(t *testing.T) {
	s, lw, hub, se, _ := apptest.ShipScene(t)
	x := mirrorOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := apptest.ProjectDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	rig.WriteFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/search-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Can this return an error instead?"})
	s.Run("review.threads.sync", nil)
	var tid core.ThreadID
	s.Until("the mirrored thread", func() bool {
		x.Wait()
		sess, _ := s.Store().State().Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Origin == core.OriginCodeHost && th.Hosted.Key == strconv.FormatInt(root.ID, 10) {
				tid = th.ID
			}
		}
		return tid != ""
	})
	ag.WaitSent(t)
	ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Good call: `Handle` could return an error for a bad config.", "proposes": true})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})

	s.Run("diff.open", registry.Args{"path": file})
	s.Until("Do it beside Forward", func() bool {
		f := s.Text()
		return strings.Contains(f, "↗ Forward to GitHub") && strings.Contains(f, "Do it")
	})
	s.ClickOn("Do it")
	if in := ag.WaitSent(t); !strings.Contains(in.Text, "Do it") {
		t.Errorf("the agent was sent %q", in.Text)
	}
	sess, _ := s.Store().State().Session(se.ID)
	th, _ := sess.Snapshot().Thread(tid)
	if last, _ := th.Last(); last.Author != core.AuthorYou || last.Body != "Do it" {
		t.Errorf("the thread ends %+v, want your Do it", last)
	}
	if p := ghPosts(hub); len(p) != 0 {
		t.Errorf("GitHub was written to: %q", p)
	}
}
