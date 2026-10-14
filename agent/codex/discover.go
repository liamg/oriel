package codex

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/agent"
)

// home is Codex's home: $CODEX_HOME, else ~/.codex; "" when there's neither.
func home() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".codex")
}

var _ agent.Discovered = (*Provider)(nil)

// Discover is every rollout Codex keeps in its home, archived ones too: sessions/YYYY/MM/DD/rollout-<time>-<thread>.jsonl.
// A compressed one (.jsonl.zst) isn't read.
func (p *Provider) Discover() []agent.Transcript {
	dir := home()
	if dir == "" {
		return nil
	}
	var out []agent.Transcript
	for _, sub := range []string{"sessions", "archived_sessions"} {
		_ = filepath.WalkDir(filepath.Join(dir, sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			id, ok := threadOf(d.Name())
			if !ok {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			out = append(out, agent.Transcript{Session: id, Logs: []agent.Log{{Path: path, Format: "codex"}}, Modified: fi.ModTime()})
			return nil
		})
	}
	return out
}

// threadOf is the thread a rollout's file name is of: rollout-YYYY-MM-DDThh-mm-ss-<thread>[_<rollout>].jsonl.
func threadOf(name string) (string, bool) {
	core, ok := strings.CutPrefix(name, "rollout-")
	if !ok {
		return "", false
	}
	core, ok = strings.CutSuffix(core, ".jsonl")
	if !ok || len(core) < 21 || core[19] != '-' {
		return "", false
	}
	id, _, _ := strings.Cut(core[20:], "_")
	return id, id != ""
}
