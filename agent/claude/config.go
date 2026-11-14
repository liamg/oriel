package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/internal/atomicfile"
)

// Claude Code's own files, which Oriel only ever merges into: a key it doesn't name is put back untouched, and a
// file that isn't valid JSON is an error, never something to overwrite.

// configEnv is CLAUDE_CONFIG_DIR as env (KEY=value) or the process sets it, or "".
func configEnv(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
}

// statePath is Claude Code's per-user state, where it records the folders the user trusts: ~/.claude.json, or
// .claude.json in CLAUDE_CONFIG_DIR.
func statePath(env []string) string {
	if dir := configEnv(env); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".claude.json")
}

// settingsPath is Claude Code's user settings.
func settingsPath(env []string) string {
	return filepath.Join(transcript.ConfigDir(env), "settings.json")
}

// trust marks dir as a trusted project, so claude started there doesn't stop at its "do you trust this folder?" or
// "allow external CLAUDE.md file imports?" dialogs, which nobody is at the PTY to answer. The imports get the
// dialog's default, No, unless the user answered already. Claude keys projects by the resolved path, so that is
// trusted too.
func trust(env []string, dir string) error {
	dirs := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		dirs = append(dirs, real)
	}
	return updateFile(statePath(env), func(m map[string]any) bool {
		projects, _ := m["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
		}
		changed := false
		for _, d := range dirs {
			p, _ := projects[d].(map[string]any)
			if p == nil {
				p = map[string]any{}
			}
			if p["hasTrustDialogAccepted"] != true {
				p["hasTrustDialogAccepted"] = true
				changed = true
			}
			if p["hasClaudeMdExternalIncludesApproved"] != true && p["hasClaudeMdExternalIncludesWarningShown"] != true {
				p["hasClaudeMdExternalIncludesWarningShown"] = true
				changed = true
			}
			projects[d] = p
		}
		m["projects"] = projects
		return changed
	})
}

// agentDefaults are settings that assume a person at the keyboard, off for an agent nobody watches type. A key
// already in the file is the user's answer and is never touched; objects merge key by key.
var agentDefaults = map[string]any{
	"skillOverrides":           map[string]any{"auto-mode-setup": "off"},
	"promptSuggestionEnabled":  false,
	"awaySummaryEnabled":       false,
	"showTurnDuration":         false,
	"skipAutoPermissionPrompt": true,
	"env": map[string]any{
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY": "1",
	},
}

// ensureDefaults merges agentDefaults into the user's settings, writing only when something was missing.
func ensureDefaults(env []string) error {
	return updateFile(settingsPath(env), func(m map[string]any) bool { return mergeMissing(m, agentDefaults) })
}

// mergeMissing writes each of src's keys into dst unless dst has it, going one level into objects both hold.
func mergeMissing(dst, src map[string]any) bool {
	changed := false
	for k, v := range src {
		cur, present := dst[k]
		if !present {
			dst[k] = clone(v)
			changed = true
			continue
		}
		sub, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if curMap, ok := cur.(map[string]any); ok && mergeMissing(curMap, sub) {
			changed = true
		}
	}
	return changed
}

func clone(v any) any {
	if m, ok := v.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = clone(val)
		}
		return out
	}
	return v
}

// updateFile edits a JSON object file under a lock, and writes it back only when edit says it changed something.
func updateFile(path string, edit func(map[string]any) bool) error {
	unlock, err := lockFile(path)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := loadFile(path)
	if err != nil {
		return err
	}
	if !edit(m) {
		return nil
	}
	return saveFile(path, m)
}

func loadFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	// Numbers as written: a float64 would round large ids on the way back.
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s is not valid JSON: data after the object", path)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func saveFile(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0o600) // a file already there keeps its mode
}

var lockWait = 10 * time.Second

// lockStale is the age at which a lock's holder must have died: no write takes this long.
const lockStale = 30 * time.Second

// lockFile serialises read-modify-writes of path across Oriel processes.
func lockFile(path string) (func(), error) {
	p := path + ".oriel.lock"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(p)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is locked by another Oriel (%s)", path, p)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
