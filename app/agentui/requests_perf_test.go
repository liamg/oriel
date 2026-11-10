//go:build perf && !race

package agentui_test

import (
	"testing"
	"time"
)

// The comment budget: a comment reaches the agent within 1 s of its ⏎; the fake agent is idle, so the host hands it
// over at once.
func TestACommentReachesTheAgentBudget(t *testing.T) {
	r, a, _ := requestsSession(t)
	const body = "Negative values round the wrong way"
	r.ClickText("New request")
	r.WaitText("Write a request")
	r.TypeText(body)
	r.WaitText(body)
	start := time.Now()
	r.TypeKeys("<CR>")
	a.WaitSent(t)
	if took := time.Since(start); took > time.Second {
		t.Errorf("the comment took %v to reach the agent", took)
	}
}
