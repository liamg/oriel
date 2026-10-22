// Package claudebox is what Claude Code needs in a sandbox: its config, settings and sign-in, its binary for the
// sandbox's platform, the environment, hosts and MCP servers it carries over from the host.
package claudebox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/sandbox"
)

// Dir is root's sandbox's CLAUDE_CONFIG_DIR, at the same path inside.
func Dir(root string) string { return sandbox.AgentDir(root, "claude") }

// tokenPath holds the `claude setup-token` token sandboxes sign in with.
func tokenPath() string { return filepath.Join(sandbox.Base(), "claude-token") }

// Token is the stored `claude setup-token` token, "" when there is none.
func Token() string {
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SaveToken stores the sign-in token, or removes it when tok is empty.
func SaveToken(tok string) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		err := os.Remove(tokenPath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.ContainsAny(tok, " \t\r\n") {
		return errors.New("that does not look like a token: it has spaces in it")
	}
	if err := os.MkdirAll(sandbox.Base(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(tokenPath(), []byte(tok+"\n"), 0o600)
}

// TokenAge is how long ago the token was saved; setup tokens last a year.
func TokenAge() (time.Duration, bool) {
	fi, err := os.Stat(tokenPath())
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// userFiles are the parts of ~/.claude a sandbox reads, read-only.
var userFiles = []string{"CLAUDE.md", "skills", "agents", "commands"}

// UserMounts are the host ~/.claude entries that exist, as host paths.
func UserMounts(hostClaude string) []string {
	var out []string
	for _, f := range userFiles {
		if _, err := os.Stat(filepath.Join(hostClaude, f)); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// droppedSettings mean something only on the host: commands at host paths,
// plugins that are not installed in the sandbox, and helpers that run on the
// host and hand over only their output.
var droppedSettings = []string{
	"hooks", "statusLine", "apiKeyHelper", "otelHeadersHelper", "awsAuthRefresh",
	"awsCredentialExport", "enabledPlugins", "extraKnownMarketplaces", "permissions",
	"sandbox", "additionalDirectories",
}

// SandboxSettings derives the sandbox's settings.json from the host's.
// helperFiles maps a helper setting to the file holding its host output;
// memory, when set, is the agent's memory directory.
func SandboxSettings(host, defaults map[string]any, helperFiles map[string]string, memory string) map[string]any {
	out := maps.Clone(host)
	if out == nil {
		out = map[string]any{}
	}
	var deny []any
	if p, ok := host["permissions"].(map[string]any); ok {
		deny, _ = p["deny"].([]any)
	}
	for _, k := range droppedSettings {
		delete(out, k)
	}
	perms := map[string]any{
		"defaultMode": "bypassPermissions",
		"allow":       []any{"mcp__oriel", "mcp__oriel-gw"},
	}
	if len(deny) > 0 {
		perms["deny"] = deny
	}
	out["permissions"] = perms
	out["skipDangerousModePermissionPrompt"] = true
	out["sandbox"] = map[string]any{"enabled": false}
	out["autoUpdates"] = false
	if memory != "" {
		out["autoMemoryDirectory"] = memory
	}
	env, _ := out["env"].(map[string]any)
	env = maps.Clone(env)
	if env == nil {
		env = map[string]any{}
	}
	for _, k := range urlEnv {
		if v, ok := env[k].(string); ok {
			env[k] = sandbox.RewriteLocalhost(v)
		}
	}
	env["DISABLE_AUTOUPDATER"] = "1"
	out["env"] = env
	for k, path := range helperFiles {
		out[k] = "cat " + path
	}
	for k, v := range defaults {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// SandboxState is the sandbox's .claude.json: first-run screens done and cwds
// trusted, since nobody is at the terminal to click through them. The host's
// own "seen it" flags come along, so a nudge the user dismissed there does
// not stall an agent here.
func SandboxState(cwds []string, host map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range host {
		if seenFlag(k) {
			out[k] = v
		}
	}
	projects := map[string]any{}
	for _, d := range cwds {
		projects[d] = map[string]any{
			"hasTrustDialogAccepted":        true,
			"hasCompletedProjectOnboarding": true,
		}
	}
	for k, v := range map[string]any{
		"hasCompletedOnboarding":        true,
		"bypassPermissionsModeAccepted": true,
		"hasSeenAutoDefaultNudge":       true,
		"autoUpdates":                   false,
		"projects":                      projects,
	} {
		out[k] = v
	}
	return out
}

// seenFlag reports a .claude.json key that only records the user having
// already seen or done something.
func seenFlag(k string) bool {
	switch k {
	case "lastOnboardingVersion", "lastReleaseNotesSeen", "tipsHistory", "tipsHistoryByCommand", "numStartups":
		return true
	}
	return strings.HasPrefix(k, "hasSeen") || strings.HasPrefix(k, "hasCompleted") ||
		strings.HasPrefix(k, "hasAcknowledged") || strings.HasPrefix(k, "hasReset")
}

// Prepare writes root's sandbox's Claude config for an agent in cwd.
func Prepare(root, cwd string, host, defaults map[string]any) error {
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	helpers := map[string]string{}
	for _, k := range []string{"apiKeyHelper", "otelHeadersHelper"} {
		cmd, _ := host[k].(string)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		out, err := runHelper(cmd)
		if err != nil {
			return fmt.Errorf("%s failed on the host: %w", k, err)
		}
		f := filepath.Join(dir, "."+k)
		if err := os.WriteFile(f, out, 0o600); err != nil {
			return err
		}
		helpers[k] = f
	}
	if err := writeJSON(filepath.Join(dir, "settings.json"), SandboxSettings(host, defaults, helpers, primaryMemory(root))); err != nil {
		return err
	}
	var hostState map[string]any
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil {
			_ = json.Unmarshal(b, &hostState)
		}
	}
	state := SandboxState([]string{cwd}, hostState)
	if cur, err := os.ReadFile(filepath.Join(dir, ".claude.json")); err == nil {
		var m map[string]any
		if json.Unmarshal(cur, &m) == nil {
			if p, ok := m["projects"].(map[string]any); ok {
				maps.Copy(p, state["projects"].(map[string]any))
				state["projects"] = p
			}
			for k, v := range m {
				if _, ok := state[k]; !ok {
					state[k] = v
				}
			}
		}
	}
	return writeJSON(filepath.Join(dir, ".claude.json"), state)
}

// primaryMemory is the memory directory of root's primary project, "" when it has none.
func primaryMemory(root string) string {
	if m := sandbox.MemoryDirs(root); len(m) > 0 {
		return m[0].Dir
	}
	return ""
}

func runHelper(cmd string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// Settings reads the host's ~/.claude/settings.json; a missing or unreadable file is an empty map.
func Settings() map[string]any {
	home, err := os.UserHomeDir()
	if err != nil {
		return map[string]any{}
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
