//go:build perf

package agentui_test

import (
	"testing"
	"time"
)

// A comment reaches the idle agent within 1 s of its ⏎.
func TestACommentReachesTheAgentBudget(t *testing.T) {
	r, a, _ := requestsSession(t)
	writeRequest(t, r, "Negative values round the wrong way")
	start := time.Now()
	r.TypeKeys("<CR>")
	a.WaitSent(t)
	if took := time.Since(start); took > time.Second {
		t.Errorf("the comment took %v to reach the agent", took)
	}
}
