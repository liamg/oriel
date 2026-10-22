package stats_test

import (
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
)

// The HUD's machine panel draws the load the machine's events report, each reading once.
func TestMachineKeepsItsLoad(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = time.Date(2026, 10, 13, 14, 0, 0, 0, time.UTC)
	m := apptest.PluginOf[*stats.Machine](s, "machine")
	for _, r := range []struct {
		cpu float64
		at  time.Time
	}{{.34, s.Now}, {.82, s.Now.Add(time.Minute)}, {.82, s.Now.Add(time.Minute)}} {
		s.Apply(core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: r.cpu, Mem: 11 << 30, MemTotal: 32 << 30, Sampled: r.at}})
		s.Frame()
	}
	if load := m.LoadOf(); !slices.Equal(load, []float64{.34, .82}) {
		t.Errorf("the machine's load, from its events: %v", load)
	}
}
