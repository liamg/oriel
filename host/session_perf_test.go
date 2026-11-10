//go:build perf

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// Shell commands come faster than the session's projects can be marked: the marks still waiting are made once, not
// one each, so a burst of commands over seven projects doesn't leave a queue of work-tree snapshots behind it.
func TestShellMarksCatchUpAfterABurstBudget(t *testing.T) {
	runs := gitRuns(t)
	r, id, a, full := sevenProjects(t, runs)
	dirs := r.session(id).Projects
	before := runs()
	for i := range 150 {
		call := fmt.Sprintf("b%d", i)
		os.WriteFile(filepath.Join(dirs[i%7].Dir, "burst.go"), []byte(fmt.Sprintf("package p // %d\n", i)), 0o644)
		a.Emit(agent.ToolCall{ID: call, Tool: "Bash", Input: json.RawMessage(`{"command":"true"}`)})
		a.Emit(agent.ToolResult{ID: call, Output: "ok"})
	}
	r.eventually(id, "the burst", func(se core.SessionState) bool {
		return slices.ContainsFunc(se.Transcript, func(it core.Item) bool { return it.ID == "b149" && it.Done })
	})
	if n := settled(r, a, runs) - before; n > 5*full {
		t.Errorf("150 commands ran git %d times, %d marks of every project; want the waiting marks made together, at most 5", n, n/full)
	}
}

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
