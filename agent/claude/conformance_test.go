package claude

import (
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
)

func TestConformance(t *testing.T) {
	agenttest.Run(t, fakeHarness(t))
}
