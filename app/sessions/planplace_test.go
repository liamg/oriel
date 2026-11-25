package sessions_test

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// A plan session opens on its plan once; after that it keeps your place: Requests in front, another session in
// front and the plan session back again, and Requests is still in front, the plan behind it.
func TestAPlanSessionKeepsYourPlaceAcrossSessions(t *testing.T) {
	w, _, _ := planSession(t)
	// on the loop, inside WaitFor
	in := func(id string) bool {
		return slices.ContainsFunc(w.Host.Layout().Shown(), func(v layout.ViewRef) bool { return v.ID == id })
	}
	shown := func(id string) bool {
		return slices.ContainsFunc(w.Shown(), func(v layout.ViewRef) bool { return v.ID == id })
	}
	w.WaitFor("the plan, the first time", func() bool { return in(sessions.PlanView) })
	se := planNow(t, w)
	w.Run("request.open", registry.Args{"thread": string(se.PlanThread())})
	w.WaitFor("the request in front", func() bool { return in(kit.RequestView) && !in(sessions.PlanView) })

	w.Run("session.dir.open", registry.Args{"dir": t.TempDir()})
	w.WaitFor("the window on the repo", func() bool { return !in(kit.RequestView) })
	w.Run("session.open", registry.Args{"id": string(se.ID)})
	w.WaitSessionOpen("rounding")
	w.WaitFor("the request back in front", func() bool { return in(kit.RequestView) || in(sessions.PlanView) })
	w.Idle()
	if shown(sessions.PlanView) {
		t.Errorf("back on the plan session, the plan came to the front: %v", w.Shown())
	}
	if !shown(kit.RequestView) {
		t.Errorf("back on the plan session, the request isn't in front: %v", w.Shown())
	}
}
