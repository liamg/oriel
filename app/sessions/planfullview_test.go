package sessions_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// within is the first node named name, and in it the first named label.
func within(s *apptest.Scene, name, label string) *render.AccessNode {
	var find func(n *render.AccessNode, want string) *render.AccessNode
	find = func(n *render.AccessNode, want string) *render.AccessNode {
		if n == nil {
			return nil
		}
		if n.Name == want {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, want); f != nil {
				return f
			}
		}
		return nil
	}
	return find(find(s.Access(), name), label)
}

// A plan thread's card in the margin opens its request in full view, and the request's Open in plan goes back to the
// plan with the thread in view.
func TestAPlanThreadOpensInFullViewAndBack(t *testing.T) {
	s := planScene(t)
	s.Run("plan.open", nil)
	s.Until("the plan", func() bool { return s.Has("PLAN.MD · REV 4") })
	card := "Thread Is half-even what finance actually wants? I'd assumed half-up."
	s.Until("the card's full view link", func() bool { return within(s, card, "Full view") != nil })
	s.Frames(2)
	s.Click(apptest.Centre(within(s, card, "Full view").Bounds), 0)
	s.Quiet()
	shown := func(id string) bool {
		return slices.ContainsFunc(s.Host().Layout().Shown(), func(v layout.ViewRef) bool { return v.ID == id })
	}
	s.Until("the request in front", func() bool { s.Frame(); return shown(kit.RequestView) && s.Find("Open in plan ↗") != nil })
	if shown(sessions.PlanView) {
		t.Errorf("the plan is still in front of the request: %v", s.Host().Layout().Shown())
	}
	s.Frames(2)
	s.Golden("plan-thread-full-view")
	s.ClickNode("Open in plan ↗")
	s.Quiet()
	s.Until("the plan back in front, the thread in it", func() bool {
		s.Frame()
		return shown(sessions.PlanView) && strings.Contains(s.Text(), "Is half-even what finance actually wants?")
	})
}
