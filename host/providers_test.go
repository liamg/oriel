package host

import (
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// A session handed over to a provider the app brings waits for it: once the app gives the host its providers, the
// session is resumed, and only once however often they're given.
func TestAHandOverWaitsForItsProvider(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("start")
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Running && se.Agent.Session == a.ID() })
	r.w.HandOver()
	r.bare = true
	r.restart()
	time.Sleep(100 * time.Millisecond)
	if n := len(r.fake.Resumed()); n != 0 {
		t.Fatalf("resumed %d times before its provider was known", n)
	}
	app := func() []agent.Provider { return []agent.Provider{r.fake} }
	r.w.SetProviders(app)
	r.agent()
	r.eventually(id, "resumed", func(se core.SessionState) bool { return se.Agent.Running })
	r.w.SetProviders(app)
	time.Sleep(100 * time.Millisecond)
	if n := len(r.fake.Resumed()); n != 1 {
		t.Errorf("resumed %d times, want once", n)
	}
	if got := r.w.Store().State().Machine.Providers; len(got) != 1 || got[0] != r.fake.ID() {
		t.Errorf("the machine's providers = %v", got)
	}
}
