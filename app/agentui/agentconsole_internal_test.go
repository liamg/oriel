package agentui

import (
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
)

// A turn that left background work running hasn't finished the work: the chip still says it's working.
func TestConsoleStatusWithBackgroundWork(t *testing.T) {
	se := core.SessionState{Agent: core.AgentState{Provider: "claude", Running: true, Background: []core.BackgroundTask{{Kind: "shell", Description: "go test ./..."}}}}
	if got, tok := consoleStatus(se); got != "working in the background" || tok != theme.StatusWorking {
		t.Errorf("chip = %q", got)
	}
}
