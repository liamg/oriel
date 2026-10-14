package app

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// agentTool runs an agent tool as a worker would, off the loop, pumping the loop while it waits.
func (s *errScene) agentTool(t *testing.T, name string, p map[string]any) string {
	t.Helper()
	out, err := s.agentToolErr(t, name, p)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (s *errScene) agentToolErr(t *testing.T, name string, p map[string]any) (string, error) {
	t.Helper()
	for _, tool := range registry.AgentTools(s.reg) {
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
		for {
			select {
			case r := <-done:
				out, _ := r.out.(string)
				return out, r.err
			default:
				s.sh.runTodo()
				s.lsp().wait()
			}
		}
	}
	t.Fatalf("no agent tool %s", name)
	return "", nil
}

// LS-3 done when: agents can call definition, references and diagnostics, read-only.
func TestAgentLSPTools(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	got := s.agentTool(t, "lsp_definition", map[string]any{"file": "main.go", "line": float64(9), "column": float64(12)})
	if !strings.HasPrefix(got, "lib.go:6:6\t") || !strings.Contains(got, "func greeting(name string) string") {
		t.Errorf("definition = %q, want lib.go:6:6 and its line", got)
	}
	got = s.agentTool(t, "lsp_references", map[string]any{"file": "main.go", "line": float64(9), "symbol": "greeting"})
	if n := strings.Count(got, "\n") + 1; n != 3 || !strings.Contains(got, "main.go:9:") {
		t.Errorf("references = %q, want 3 of them", got)
	}
	got = s.agentTool(t, "lsp_diagnostics", map[string]any{"file": "main.go"})
	if !strings.Contains(got, "main.go:") || !strings.Contains(got, "greting") {
		t.Errorf("diagnostics = %q, want the undefined greting", got)
	}
	if _, err := s.agentToolErr(t, "lsp_definition", map[string]any{"file": "main.go", "line": float64(9), "symbol": "nope"}); err == nil ||
		!strings.Contains(err.Error(), "isn't on line 9") {
		t.Errorf("a symbol that isn't there: %v", err)
	}
}
