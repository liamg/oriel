package ship_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

func githubOf(s *apptest.Scene) *ship.GitHub { return apptest.PluginOf[*ship.GitHub](s, "github") }

// The agent never posts; the user's forward does. GitHub threads are mirrored inline, the agent drafts a reply, and
// you forward it: "GitHub review · L80 · @priya-k · changes requested · synced 30s ago", the reviewer's comment,
// "Claude draft · not sent", "↗ Forward to GitHub", "Edit", "Claude never posts to GitHub. You do.", and the status
// line's "GH 1 changes requested".
func TestGitHubReviewForward(t *testing.T) {
	s, lw, hub, se, _ := shipScene(t)
	x := githubOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	apiDir := shipDir(se, "search-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
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
	if batch := ag.WaitSent(t).Text; !strings.Contains(batch, "Can this return an error instead?") || !strings.Contains(batch, `origin="github"`) {
		t.Fatalf("the agent was given %q", batch)
	}
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Agreed. `Handle` now returns an error."}); !strings.Contains(out, "drafted") {
		t.Errorf("reply: %q", out)
	}
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})

	s.Run("diff.open", registry.Args{"path": file})
	s.Until("the GitHub block between the diff's lines", func() bool {
		f := s.Text()
		return strings.Contains(f, "GitHub review") && strings.Contains(f, "↗ Forward to GitHub")
	})
	s.WantFrame("GitHub review", "· L3 ·", "@PRIYA-K", "CHANGES REQUESTED", "synced", "priya-k", "Can this return an error instead?",
		"draft · not sent", "Edit", "Fake never posts to GitHub. You do.", "GH 1 changes requested")
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
	if d := draftOf(t, s, se.ID, tid); d.Body != "Agreed. `Handle` now returns an error. Logged too." || !d.Draft {
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
	if err := s.Host().Run(context.Background(), "github.forward", registry.Args{"thread": string(tid)}); err == nil {
		t.Error("forwarding again went through")
	}

	// The echo doesn't come back as another comment.
	s.Run("github.sync", nil)
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

func draftOf(t *testing.T, s *apptest.Scene, id core.SessionID, tid core.ThreadID) core.Comment {
	t.Helper()
	st := s.Store().State()
	sess, _ := st.Session(id)
	th, _ := sess.Snapshot().Thread(tid)
	if d := th.Drafts(); len(d) == 1 {
		return d[0]
	}
	t.Fatalf("no draft in %+v", th.Comments)
	return core.Comment{}
}
