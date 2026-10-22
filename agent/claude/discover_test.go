package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// Discover finds each session's log under $CLAUDE_CONFIG_DIR's projects, with its subagents' logs beside it, and
// not the subagents' as sessions of their own.
func TestDiscoverFindsTranscripts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	for _, f := range []string{
		"projects/-w-api/s1.jsonl",
		"projects/-w-api/s1/subagents/agent-a1.jsonl",
		"projects/-w-web/s2.jsonl",
		"todos/s1.json",
	} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]int{}
	for _, tr := range (&Provider{}).Discover() {
		got[tr.Session] = len(tr.Logs)
		if tr.Logs[0].Path != filepath.Join(dir, "projects", filepath.Base(filepath.Dir(tr.Logs[0].Path)), tr.Session+".jsonl") || tr.Modified.IsZero() {
			t.Errorf("transcript = %+v", tr)
		}
	}
	if len(got) != 2 || got["s1"] != 2 || got["s2"] != 1 {
		t.Errorf("found %v, want s1 with its subagent and s2", got)
	}
}
