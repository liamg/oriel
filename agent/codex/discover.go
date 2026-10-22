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

// Discover is every rollout Codex keeps in its home, archived ones too: sessions/YYYY/MM/DD/rollout-<time>-<thread>.jsonl,
// or .jsonl.zst once Codex has compressed it; a plain one hides its compressed sibling.
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
			if plain, zst := strings.CutSuffix(path, ".zst"); zst {
				if _, err := os.Stat(plain); err == nil {
					return nil
				}
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

// threadOf is the thread a rollout's file name is of: rollout-YYYY-MM-DDThh-mm-ss-<thread>[_<rollout>].jsonl[.zst].
func threadOf(name string) (string, bool) {
	core, ok := strings.CutPrefix(name, "rollout-")
	if !ok {
		return "", false
	}
	core, ok = strings.CutSuffix(strings.TrimSuffix(core, ".zst"), ".jsonl")
	if !ok || len(core) < 21 || core[19] != '-' {
		return "", false
	}
	id, _, _ := strings.Cut(core[20:], "_")
	return id, id != ""
}
