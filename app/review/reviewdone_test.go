package review_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Closing the tab of a review you've approved offers to end it, its local branch ticked to go; ⌘↵ ends it.
func TestClosingAnApprovedReviewOffersToEndIt(t *testing.T) {
	s, lw, hub, pr, id, _ := prReviewScene(t, false)
	a := lw.Fake.WaitStarted(t)
	for a.Spec.Pack.Name != "review" {
		a = lw.Fake.WaitStarted(t)
	}
	s.Until("the PR's diff", func() bool { return s.Showing(kit.DiffView) })
	s.Run("ship.pr.open", nil)
	s.Until("the review tab", func() bool { return s.Has("YOUR REVIEW") })
	s.Run("review.pr.preview", registry.Args{"id": string(id), "verdict": "approve"})
	s.Until("the approval's preview", func() bool { return s.Has("Post: Approved") })
	s.ClickText("Post: Approved")
	s.Until("the approval on GitHub", func() bool {
		rs := hub.Reviews("acme/api", pr.Number)
		return len(rs) == 1 && rs[0].State == gh.ReviewApproved
	})
	s.UntilState("the approval kept", func(st core.State) bool {
		sess, _ := st.Session(id)
		d, _ := core.DataOf[core.ReviewData](sess.Snapshot())
		return d.Draft != nil && len(d.Draft.Sent) == 1
	})

	sess, _ := s.Store().State().Session(id)
	s.Until("its tab", func() bool { return s.FindRole(render.RoleTab, sess.Name()) != nil })
	s.RightClick(apptest.Centre(s.NodeRole(render.RoleTab, sess.Name()).Bounds))
	s.ClickText("Close tab")
	s.Until("the End session dialog", func() bool { return s.Has("END SESSION", "You approved") })
	if b := s.Node("Delete the local branches too (remote branches stay)"); b.Checked == nil || !*b.Checked {
		t.Error("deleting the local branch isn't ticked")
	}
	s.Press("<D-CR>")
	s.UntilState("the review ended", func(st core.State) bool {
		sess, ok := st.Session(id)
		return !ok || sess.Snapshot().Archived
	})
}

// Once you've approved, the session's work is done: the review's own comments, mirrored back from GitHub, aren't the
// agent's to answer, so its tab shows ✓, not a spinner.
func TestApprovedReviewIsQuiet(t *testing.T) {
	s, lw, hub, pr, id, _ := prReviewScene(t, false)
	a := lw.Fake.WaitStarted(t)
	for a.Spec.Pack.Name != "review" {
		a = lw.Fake.WaitStarted(t)
	}
	a.Call(t, "comment", map[string]any{"file": "bucket.go", "line": 3, "severity": "high", "body": "Takes one whatever the cost."})
	sess, _ := s.Store().State().Session(id)
	a.Call(t, "reply", map[string]any{"thread": string(sess.Snapshot().Threads[0].ID), "body": "Looks fine bar the cost.", "resolve": true})
	s.Run("ship.pr.open", nil)
	s.Until("the review tab", func() bool { return s.Has("YOUR REVIEW") })
	s.Run("review.pr.preview", registry.Args{"id": string(id), "verdict": "approve"})
	s.Until("the approval's preview", func() bool { return s.Has("Post: Approved") })
	s.ClickText("Post: Approved")
	s.Until("the approval on GitHub", func() bool {
		rs := hub.Reviews("acme/api", pr.Number)
		return len(rs) == 1 && rs[0].State == gh.ReviewApproved
	})
	s.UntilState("the posted comment mirrored back, and the session quiet", func(st core.State) bool {
		sess, _ := st.Session(id)
		se := sess.Snapshot()
		mirrored := false
		for _, th := range se.Threads {
			mirrored = mirrored || th.Origin == core.OriginGitHub && len(th.Comments) > 0
		}
		return mirrored && sess.Status() == core.StatusQuiet
	})
}
