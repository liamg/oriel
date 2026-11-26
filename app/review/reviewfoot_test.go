package review_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// A comment for the author, opened in Requests, says where it stands in your review (YOUR REVIEW, its severity, Keep
// and Drop) under the comment's text, not out at the panel's left edge.
func TestYourReviewSitsUnderTheCommentInRequests(t *testing.T) {
	s, lw, _, _, id, _ := prReviewScene(t, false)
	a := lw.Fake.WaitStarted(t)
	for a.Spec.Pack.Name != "review" {
		a = lw.Fake.WaitStarted(t)
	}
	a.Call(t, "comment", map[string]any{"file": "bucket.go", "line": 1, "severity": "nit", "body": "Name the package for what it limits."})
	var tid core.ThreadID
	s.UntilState("the reviewer's comment", func(st core.State) bool {
		sess, _ := st.Session(id)
		for _, th := range sess.Snapshot().Threads {
			if len(th.Comments) > 0 && th.Comments[0].Body == "Name the package for what it limits." {
				tid = th.ID
			}
		}
		return tid != ""
	})
	s.Run("request.open", registry.Args{"thread": string(tid)})
	s.Until("the comment and where it stands", func() bool { return s.Has("Name the package for what it limits.", "YOUR REVIEW") })
	said, foot := lastRunX(s, "Name the package for what it limits."), lastRunX(s, "YOUR REVIEW") // the comment, not the thread's title over it
	if foot < said || foot > said+24 {
		t.Errorf("YOUR REVIEW starts at x=%v, want it under the comment's text at x=%v", foot, said)
	}
}

// lastRunX is where the last run of text reading text starts across; -1 if none is drawn.
func lastRunX(s *apptest.Scene, text string) float32 {
	x := float32(-1)
	if s.List() == nil {
		return x
	}
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			x = op.At.X
		}
	}
	return x
}
