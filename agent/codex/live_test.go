package codex

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// TestLive runs the real codex, signed in with ChatGPT, through the same harness. ORIEL_LIVE=1 runs it.
func TestLive(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("ORIEL_LIVE=1 runs the real codex")
	}
	agenttest.Run(t, agenttest.Harness{
		Provider: &Provider{OrielBin: self(t)},
		Spec: func(t *testing.T, dir string) agent.StartSpec {
			return agent.StartSpec{StateDir: t.TempDir(), Policy: agent.PolicyAsk}
		},
		Timeout: 3 * time.Minute,
	})
}
