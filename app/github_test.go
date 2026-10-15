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

func (s *errScene) github() *githubExt {
	for _, e := range s.exts {
		if x, ok := e.(*githubExt); ok {
			return x
		}
	}
	return nil
}

// RV-4 done when: "the agent never posts; the user's forward does". ui.md 06: "GitHub threads mirrored inline. The
// agent drafts a reply, and you forward it"; 06's block: "GitHub review · L80 · @priya-k · changes requested · synced
// 30s ago", the reviewer's comment, "Claude draft · not sent", "↗ Forward to GitHub", "Edit", "Claude never posts to
// GitHub. You do.", and the status line's "GH 1 changes requested".
func TestGitHubReviewForward(t *testing.T) {
	s, lw, hub, se, _ := shipScene(t)
	x := s.github()
	ag := lw.fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	ag.Emit(agent.TurnEnd{})
	apiDir := shipDir(se, "usage-api")
	file := filepath.Join(apiDir, "internal/api/usage/handler.go")
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() int { return 2 }\n"})
	var branch string
	for _, p := range se.Projects {
		if p.Name == "usage-api" {
			branch = p.Branch
		}
	}
	pr := hub.AddPR("acme/usage-api", gh.PR{Title: "feat(api): usage", Head: branch, Base: "main"})
	root := hub.AddReviewComment("acme/usage-api", pr.Number, gh.ReviewComment{User: "priya-k", Association: "MEMBER",
		Path: "internal/api/usage/handler.go", Line: 3, Body: "Silently defaulting to 2 will hide config mistakes. Can this return an error instead?"})
	hub.AddReview("acme/usage-api", pr.Number, gh.Review{User: "priya-k", Association: "MEMBER", State: gh.ReviewChangesRequested})

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
	if batch := ag.WaitSent(t).Text; !strings.Contains(batch, "Can this return an error instead?") || !strings.Contains(batch, `origin="github"`) {
		t.Fatalf("the agent was given %q", batch)
	}
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Agreed. `Handle` now returns an error."}); !strings.Contains(out, "drafted") {
		t.Errorf("reply: %q", out)
	}
	ag.Emit(agent.TurnEnd{})

	s.run("diff.open", registry.Args{"path": file})
	s.until(t, "the GitHub block between the diff's lines", func() bool {
		f := s.frameText()
		return strings.Contains(f, "GitHub review") && strings.Contains(f, "↗ Forward to GitHub")
	})
	s.wantFrame(t, "GitHub review", "· L3 ·", "@PRIYA-K", "CHANGES REQUESTED", "synced", "priya-k", "Can this return an error instead?",
		"draft · not sent", "Edit", "Fake never posts to GitHub. You do.", "GH 1 changes requested")
	s.lint("a GitHub thread in the diff")
	if o := s.ui.Overflowing(); len(o) > 0 {
		t.Errorf("text drawn past its box:\n%s", strings.Join(o, "\n"))
	}
	if p := ghPosts(hub); len(p) != 0 {
		t.Fatalf("GitHub was written to before the forward: %q", p)
	}

	// Edit, then save: the draft changes, still unsent.
	s.click(centre(s.access("Edit").Bounds))
	s.until(t, "the draft editor", func() bool { return s.countAccess("Edit the draft reply") > 0 })
	s.typeInto(t, "Edit the draft reply", " Logged too.")
	s.click(centre(s.access("Save draft").Bounds))
	s.until(t, "the edited draft", func() bool {
		return strings.Contains(s.frameText(), "Logged too.") && s.countAccess("Edit the draft reply") == 0
	})
	if d := draftOf(t, s, se.ID, tid); d.Body != "Agreed. `Handle` now returns an error. Logged too." || !d.Draft {
		t.Errorf("the edited draft %+v", d)
	}

	// Forward: posted once, as the user, and shown as sent.
	s.click(centre(s.access("↗ Forward to GitHub").Bounds))
	s.until(t, "the forward", func() bool {
		x.wait()
		return strings.Contains(s.frameText(), "sent to github")
	})
	if p := ghPosts(hub); len(p) != 1 || !strings.HasSuffix(p[0], "/replies") {
		t.Fatalf("posts %q, want one reply", p)
	}
	cs := hub.ReviewComments("acme/usage-api", pr.Number)
	if last := cs[len(cs)-1]; last.Body != "Agreed. `Handle` now returns an error. Logged too." || last.User != "you" {
		t.Errorf("on GitHub: %+v", last)
	}
	if strings.Contains(s.frameText(), "↗ Forward to GitHub") {
		t.Error("a sent reply still offers Forward")
	}
	if err := s.host.Run(context.Background(), "github.forward", registry.Args{"thread": string(tid)}); err == nil {
		t.Error("forwarding again went through")
	}

	// The echo doesn't come back as another comment.
	s.run("github.sync", nil)
	x.wait()
	st := s.store.State()
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

func draftOf(t *testing.T, s *errScene, id core.SessionID, tid core.ThreadID) core.Comment {
	t.Helper()
	st := s.store.State()
	sess, _ := st.Session(id)
	th, _ := sess.Snapshot().Thread(tid)
	if d := th.Drafts(); len(d) == 1 {
		return d[0]
	}
	t.Fatalf("no draft in %+v", th.Comments)
	return core.Comment{}
}
