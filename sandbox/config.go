package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the sandbox's settings. The worker reads them from Oriel's
// settings and hands them over; nothing here loads them itself.
type Config struct {
	// Default is how new sessions run: "sandbox" (the default) or "host".
	Default string `json:"default,omitempty"`
	// Image replaces the base image for every project.
	Image string `json:"image,omitempty"`
	// ClaudeVersion pins the agent's Claude Code: "" runs the host's.
	ClaudeVersion string `json:"claudeVersion,omitempty"`
	// Env names the host variables a sandbox inherits; nil means the defaults.
	Env []string `json:"env,omitempty"`
	// Rules are the global gateway rules.
	Rules []PolicyRule `json:"rules,omitempty"`
	// Egress are the global network rules, on top of the preset allowlist.
	Egress []EgressRule `json:"egress,omitempty"`
	// Projects holds per-repository overrides, keyed by the repo's path.
	Projects map[string]ProjectConfig `json:"projects,omitempty"`
	// AutoAllowReadOnly allows upstream tools that declare readOnlyHint.
	AutoAllowReadOnly bool `json:"autoAllowReadOnly,omitempty"`
	// ApprovalSecs is how long a gateway call waits on a person.
	ApprovalSecs int `json:"approvalSecs,omitempty"`
	// Hidden are upstream MCP servers sandboxes never see.
	Hidden []string `json:"hidden,omitempty"`
	// NoEgressProxy turns off network filtering: containers get plain internet.
	NoEgressProxy bool `json:"noEgressProxy,omitempty"`
	// Memory caps each container's memory ("8g"; "none" for no cap).
	Memory string `json:"memory,omitempty"`
	// PidsLimit caps each container's processes (-1 for no cap).
	PidsLimit int `json:"pidsLimit,omitempty"`
	// Servers are extra MCP servers for the gateway, in Claude's mcpServers
	// format, beside the ones Claude Code has installed.
	Servers map[string]json.RawMessage `json:"servers,omitempty"`
	// Dismissed are recommended servers the user said no to.
	Dismissed []string `json:"dismissed,omitempty"`
}

// ProjectConfig overrides the sandbox settings for one repository.
type ProjectConfig struct {
	Rules  []PolicyRule `json:"rules,omitempty"`
	Egress []EgressRule `json:"egress,omitempty"`
	// Image is the project's own image.
	Image string `json:"image,omitempty"`
	// Dockerfile is the sha256 of the repo's .oriel/sandbox.Dockerfile the
	// user approved; a different file waits for approval again.
	Dockerfile string `json:"dockerfile,omitempty"`
}

// PolicyRule rules on gateway tools matching Tool, a name or a glob.
type PolicyRule struct {
	Tool   string `json:"tool"`
	Action string `json:"action"` // allow | ask | deny
}

// EgressRule rules on a network destination. Host is a name or "*.domain".
type EgressRule struct {
	Host  string `json:"host"`
	Port  int    `json:"port,omitempty"`
	Allow bool   `json:"allow"`
	// Private lets an allow reach private and tailnet addresses.
	Private bool `json:"private,omitempty"`
}

// SandboxDefault is how a new session runs when nothing says otherwise.
func (c Config) SandboxDefault() string {
	if c.Default == ModeHost {
		return ModeHost
	}
	return ModeSandbox
}

// ClaudeSettings reads the host's ~/.claude/settings.json; a missing or
// unreadable file is an empty map.
func ClaudeSettings() map[string]any {
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
