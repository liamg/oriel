package app_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
)

// A session started while the app is open has a freshness manager of its own, as one there when it opened has: the
// host puts it in the store whole (SessionSynced), with no SessionOpened.
func TestASessionStartedLaterHasItsManager(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "later", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(lw.Repo)}})
	if err != nil {
		t.Fatal(err)
	}
	lw.Fake.WaitStarted(t)
	s.Until("its manager", func() bool { return kit.ManagerFor(s.Registry(), id) != nil })
}
