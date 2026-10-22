package stats_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/render"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/ui/widget"
)

// usageFails is a provider whose /usage probe fails with err.
type usageFails struct {
	*agenttest.Fake
	err error
}

func (f usageFails) Usage(context.Context) (core.ProviderUsage, error) {
	return core.ProviderUsage{}, f.err
}

// The Usage page says why a probe got no windows, in words, not the probe's timeout.
func TestUsageSaysWhyTheProbeFailed(t *testing.T) {
	for _, tc := range []struct {
		hello bool
		want  string
	}{
		{false, "Claude didn't start for the usage check"},
		{true, "Claude didn't show usage: a dialog may be in the way"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
			t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
			fake := agenttest.NewFake()
			fake.Name = "claude"
			providers := &agent.Providers{}
			providers.Register(usageFails{Fake: fake, err: &claude.ProbeError{Hello: tc.hello, Tries: 3, Err: context.DeadlineExceeded}})
			w, err := host.New(host.Options{Home: t.TempDir(), Providers: providers, Exec: rig.NopExec{}, Load: rig.SteadyLoad})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { w.Close() })
			s := apptest.New(t, apptest.Options{Config: apptest.Connect(w)})
			x := apptest.PluginOf[*stats.Plugin](s, "stats")
			s.Run("usage.open", nil)
			s.Run("usage.refresh", nil)
			deadline := time.Now().Add(10 * time.Second)
			for x.Reading().Phase == widget.Pending && time.Now().Before(deadline) {
				s.Frame()
				s.RunQueued()
				time.Sleep(10 * time.Millisecond)
			}
			s.Frame()
			s.Frame()
			s.WantFrame(tc.want)
			if text := s.Text(); strings.Contains(text, "deadline") || strings.Contains(text, "timed out") {
				t.Errorf("the page shows the probe's raw error:\n%s", text)
			}
			// Resting on it shows what the probe said.
			s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint(tc.want)})
			s.Frame()
			s.Now = s.Now.Add(time.Second)
			time.Sleep(600 * time.Millisecond)
			s.Frame()
			s.WantFrame((&claude.ProbeError{Hello: tc.hello, Tries: 3, Err: context.DeadlineExceeded}).Error())
		})
	}
}
