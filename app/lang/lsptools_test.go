package lang_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// agentTool runs an agent tool as a host would, off the loop, pumping the loop while it waits.
func agentTool(t *testing.T, s *apptest.Scene, name string, p map[string]any) string {
	t.Helper()
	out, err := agentToolErr(t, s, name, p)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func agentToolErr(t *testing.T, s *apptest.Scene, name string, p map[string]any) (string, error) {
	t.Helper()
	for _, tool := range registry.AgentTools(s.Registry()) {
		if tool.Name != name {
			continue
		}
		if !tool.ReadOnly {
			t.Errorf("%s isn't read-only", name)
		}
		type result struct {
			out any
			err error
		}
		done := make(chan result, 1)
		go func() {
			out, err := tool.Run(registry.WithSession(context.Background(), "s1"), p)
			done <- result{out, err}
		}()
		var r result
		s.Until("the tool's answer", func() bool {
			lspOf(s).Wait()
			select {
			case r = <-done:
				return true
			default:
				return false
			}
		})
		out, _ := r.out.(string)
		return out, r.err
	}
	t.Fatalf("no agent tool %s", name)
	return "", nil
}

// Agents can call definition, references and diagnostics, read-only.
func TestAgentLSPTools(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	got := agentTool(t, s, "lsp_definition", map[string]any{"file": "main.go", "line": float64(9), "column": float64(12)})
	if !strings.HasPrefix(got, "lib.go:6:6\t") || !strings.Contains(got, "func greeting(name string) string") {
		t.Errorf("definition = %q, want lib.go:6:6 and its line", got)
	}
	got = agentTool(t, s, "lsp_references", map[string]any{"file": "main.go", "line": float64(9), "symbol": "greeting"})
	if n := strings.Count(got, "\n") + 1; n != 3 || !strings.Contains(got, "main.go:9:") {
		t.Errorf("references = %q, want 3 of them", got)
	}
	got = agentTool(t, s, "lsp_diagnostics", map[string]any{"file": "main.go"})
	if !strings.Contains(got, "main.go:") || !strings.Contains(got, "greting") {
		t.Errorf("diagnostics = %q, want the undefined greting", got)
	}
	if _, err := agentToolErr(t, s, "lsp_definition", map[string]any{"file": "main.go", "line": float64(9), "symbol": "nope"}); err == nil ||
		!strings.Contains(err.Error(), "isn't on line 9") {
		t.Errorf("a symbol that isn't there: %v", err)
	}
}
