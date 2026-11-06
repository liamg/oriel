package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
)

var (
	_ agent.Discovered      = (*Provider)(nil)
	_ agent.DiscoverStamped = (*Provider)(nil)
)

// DiscoverStamp is when each project folder last had a log added or removed: a folder's own time moves with its
// entries, so a new session's log, or a subagent's folder, changes it.
func (*Provider) DiscoverStamp() string {
	projects := filepath.Join(transcript.ConfigDir(nil), "projects")
	ents, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			fmt.Fprintf(&b, "%s %d\n", e.Name(), fi.ModTime().UnixNano())
		}
	}
	return b.String()
}

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
