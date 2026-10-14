package codex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Discover finds every rollout under $CODEX_HOME, by the thread its name carries, and nothing else.
//
// ref:   codex/codex-rs/rollout/src/list.rs
// quote: "Directory layout: `~/.codex/sessions/YYYY/MM/DD/rollout-YYYY-MM-DDThh-mm-ss-<uuid>.jsonl`"
// ref:   codex/codex-rs/rollout/src/rollout_file_name.rs
// quote: "let (thread_id, rollout_id) = ids.split_once('_').unwrap_or((ids, ids));"
// ref:   codex/codex-rs/rollout/src/lib.rs
// quote: "pub const ARCHIVED_SESSIONS_SUBDIR: &str = \"archived_sessions\";"
func TestDiscoverFindsRollouts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	files := map[string]string{
		"sessions/2026/10/01/rollout-2026-10-01T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a53.jsonl":                                      "0199a213-81c0-7800-8aa1-bbab2a035a53",
		"sessions/2026/10/02/rollout-2026-10-02T10-30-00-0199a213-81c0-7800-8aa1-bbab2a035a54_0199a213-81c0-7800-8aa1-bbab2a035a99.jsonl": "0199a213-81c0-7800-8aa1-bbab2a035a54",
		"archived_sessions/rollout-2026-09-01T08-00-00-0199a213-81c0-7800-8aa1-bbab2a035a55.jsonl":                                        "0199a213-81c0-7800-8aa1-bbab2a035a55",
		"sessions/2026/10/01/rollout-2026-10-01T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a56.jsonl.zst":                                  "",
		"sessions/2026/10/01/notes.jsonl": "",
		"history.jsonl":                   "",
	}
	for f := range files {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, tr := range (&Provider{}).Discover() {
		if len(tr.Logs) != 1 || tr.Logs[0].Format != "codex" || tr.Modified.IsZero() {
			t.Errorf("transcript = %+v", tr)
		}
		rel, _ := filepath.Rel(dir, tr.Logs[0].Path)
		if files[rel] != tr.Session {
			t.Errorf("%s: session %q, want %q", rel, tr.Session, files[rel])
		}
		got = append(got, tr.Session)
	}
	slices.Sort(got)
	if len(got) != 3 {
		t.Errorf("found %v, want the three rollouts", got)
	}
}
