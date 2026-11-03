package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
)

var _ agent.Discovered = (*Provider)(nil)

// Discover is every session log in Claude Code's config directory ($CLAUDE_CONFIG_DIR, else ~/.claude):
// projects/<slug>/<id>.jsonl, each with its subagents' logs.
func (*Provider) Discover() []agent.Transcript {
	mains, _ := filepath.Glob(filepath.Join(transcript.ConfigDir(nil), "projects", "*", "*.jsonl"))
	var out []agent.Transcript
	for _, main := range mains {
		fi, err := os.Stat(main)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		t := agent.Transcript{Session: strings.TrimSuffix(filepath.Base(main), ".jsonl"), Modified: fi.ModTime(),
			Logs: []agent.Log{{Path: main, Format: "claude"}}}
		for _, p := range transcript.Subagents(main) {
			if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t.Modified) {
				t.Modified = fi.ModTime()
			}
			t.Logs = append(t.Logs, agent.Log{Path: p, Format: "claude"})
		}
		out = append(out, t)
	}
	return out
}
