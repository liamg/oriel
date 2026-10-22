package apptest

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// StatsClock has the stats plugin read the time from now, in loc: a scene's fixed clock.
func (s *Scene) StatsClock(now func() time.Time, loc *time.Location) {
	x := PluginOf[*stats.Plugin](s, "stats")
	x.Now, x.Loc = now, loc
}

// SettleStats frames until the stats plugin has read what it was asked for.
func (s *Scene) SettleStats() {
	s.T.Helper()
	x := PluginOf[*stats.Plugin](s, "stats")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.Frame()
		s.RunQueued()
		if !x.Busy() {
			s.Frame()
			s.RunQueued()
			if !x.Busy() {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.T.Fatal("the stats never settled")
}

// StatsScene is the app on the stats rig's session host, on the rig's clock, with pricing-rounding in front: the
// stats plugin reads the scene's clock, in UTC.
func StatsScene(t *testing.T) (*Scene, *rig.Stats, *stats.Plugin) {
	t.Helper()
	r := rig.NewStats(t)
	r.Build()
	r.Readings(t)
	work := ""
	if se, ok := r.W.Store().Session(r.IDs["pricing-rounding"]); ok {
		work = kit.SessionDir(se.Snapshot()) // the window a session in front opens on
	}
	s := New(t, Options{Config: func(cfg *Config) { Connect(r.W)(cfg); cfg.WorkDir = work }})
	s.Now = rig.StatsNow
	x := PluginOf[*stats.Plugin](s, "stats")
	x.Now, x.Loc = func() time.Time { return s.Now }, time.UTC
	rig.WaitState(t, s.Store(), "the sessions and readings", func(st core.State) bool {
		return !st.Machine.Sampled.IsZero() && len(st.Sessions) == len(rig.FxSessions) && len(st.Usage) == 2 &&
			st.Usage[0].Sampled.After(rig.StatsNow.Add(-time.Minute))
	})
	s.Apply(core.SessionFocused{ID: r.IDs["pricing-rounding"]})
	if err := s.Registry().Options().Set(stats.AccountsOption, registry.LayerUser, registry.At{}, map[string]string{rig.WorkAccount: "Work"}); err != nil {
		t.Fatal(err)
	}
	return s, r, x
}
