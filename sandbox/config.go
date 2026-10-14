package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the sandbox's settings: the app resolves its sandbox.* options
// into one and sends it to the worker; nothing here loads them itself.
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

// ParseEgressRule reads a network rule written "allow|deny host[:port] [private]": "allow *.golang.org:443".
func ParseEgressRule(s string) (EgressRule, error) {
	f := strings.Fields(s)
	if len(f) < 2 || len(f) > 3 {
		return EgressRule{}, fmt.Errorf("%q isn't a network rule: allow|deny host[:port] [private]", s)
	}
	var r EgressRule
	switch f[0] {
	case "allow":
		r.Allow = true
	case "deny":
	default:
		return EgressRule{}, fmt.Errorf("%q: a network rule starts allow or deny, not %q", s, f[0])
	}
	r.Host = f[1]
	if h, p, ok := strings.Cut(f[1], ":"); ok {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return EgressRule{}, fmt.Errorf("%q: %q isn't a port", s, p)
		}
		r.Host, r.Port = h, n
	}
	if r.Host == "" {
		return EgressRule{}, fmt.Errorf("%q has no host", s)
	}
	if len(f) == 3 {
		if f[2] != "private" || !r.Allow {
			return EgressRule{}, fmt.Errorf("%q: only an allow ends with private", s)
		}
		r.Private = true
	}
	return r, nil
}

// String is the rule as ParseEgressRule reads it.
func (r EgressRule) String() string {
	s := "deny "
	if r.Allow {
		s = "allow "
	}
	s += r.Host
	if r.Port > 0 {
		s += ":" + strconv.Itoa(r.Port)
	}
	if r.Private {
		s += " private"
	}
	return s
}

// ParsePolicyRule reads a gateway rule written "allow|ask|deny tool", the tool a name or a glob: "deny oriel__push".
func ParsePolicyRule(s string) (PolicyRule, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return PolicyRule{}, fmt.Errorf("%q isn't a gateway rule: allow|ask|deny tool", s)
	}
	switch f[0] {
	case "allow", "ask", "deny":
	default:
		return PolicyRule{}, fmt.Errorf("%q: a gateway rule starts allow, ask or deny, not %q", s, f[0])
	}
	return PolicyRule{Tool: f[1], Action: f[0]}, nil
}

// String is the rule as ParsePolicyRule reads it.
func (r PolicyRule) String() string { return r.Action + " " + r.Tool }
