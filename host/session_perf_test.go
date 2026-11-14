//go:build perf

package host

import (
	"context"
	"testing"
	"time"
)

// A comment reaches an idle agent within 1 s.
func TestACommentReachesTheAgentBudget(t *testing.T) {
	r, _, a, req := idleWithAComment(t)
	start := time.Now()
	if _, err := r.w.Comment(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	a.WaitSent(t)
	if d := time.Since(start); d > time.Second {
		t.Errorf("the comment took %v to reach the agent", d)
	}
}
