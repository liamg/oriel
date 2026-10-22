package core

import (
	"testing"
	"time"
)

// A stop records who did it while the agent runs or starts; one already stopped keeps who stopped it.
func TestStoppedSaysWho(t *testing.T) {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		a    AgentState
		want string
	}{
		{"running", AgentState{Running: true}, StoppedByYou},
		{"starting", AgentState{Starting: &AgentStart{Step: "Starting the agent"}}, StoppedByYou},
		{"stopped", AgentState{StoppedBy: StoppedByAgent}, StoppedByAgent},
	} {
		c.a.Stopped(StoppedByYou, at)
		if c.a.StoppedBy != c.want || c.a.Live() {
			t.Errorf("%s: stopped by %q, live %v; want %q", c.name, c.a.StoppedBy, c.a.Live(), c.want)
		}
	}
}
