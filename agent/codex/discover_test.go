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
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "Returns the existing rollout path, preferring the plain `.jsonl` file over"
func TestDiscoverFindsRollouts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	files := map[string]string{
		"sessions/2026/10/01/rollout-2026-10-01T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a53.jsonl":                                      "0199a213-81c0-7800-8aa1-bbab2a035a53",
		"sessions/2026/10/02/rollout-2026-10-02T10-30-00-0199a213-81c0-7800-8aa1-bbab2a035a54_0199a213-81c0-7800-8aa1-bbab2a035a99.jsonl": "0199a213-81c0-7800-8aa1-bbab2a035a54",
		"archived_sessions/rollout-2026-09-01T08-00-00-0199a213-81c0-7800-8aa1-bbab2a035a55.jsonl":                                        "0199a213-81c0-7800-8aa1-bbab2a035a55",
		"sessions/2026/10/01/rollout-2026-10-01T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a56.jsonl.zst":                                  "0199a213-81c0-7800-8aa1-bbab2a035a56",
		"sessions/2026/10/03/rollout-2026-10-03T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a57.jsonl":                                      "0199a213-81c0-7800-8aa1-bbab2a035a57",
		"sessions/2026/10/03/rollout-2026-10-03T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a57.jsonl.zst":                                  "",
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
	if len(got) != 5 {
		t.Errorf("found %v, want the five rollouts", got)
	}
}

// A thread's rollout that Codex has compressed since, once a week cold, is its log still, at its compressed path.
//
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "Opens a rollout line reader that transparently handles plain `.jsonl` and `.jsonl.zst` files."
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "const MIN_ROLLOUT_AGE: Duration = Duration::from_secs(7 * 24 * 60 * 60);"
func TestLogsFollowCompression(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	id := "0199a213-81c0-7800-8aa1-bbab2a035a53"
	p := filepath.Join(dir, "sessions/2026/10/01/rollout-2026-10-01T09-00-00-"+id+".jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if logs := (&Provider{}).Logs("/w", id); len(logs) != 1 || logs[0].Path != p {
		t.Fatalf("logs = %+v", logs)
	}
	if err := os.Rename(p, p+".zst"); err != nil {
		t.Fatal(err)
	}
	if logs := (&Provider{}).Logs("/w", id); len(logs) != 1 || logs[0].Path != p+".zst" || logs[0].Format != "codex" {
		t.Fatalf("logs once compressed = %+v", logs)
	}
}
