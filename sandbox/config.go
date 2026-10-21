package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Config is the sandbox's settings: the app resolves its sandbox.* options
// into one and sends it to the worker; nothing here loads them itself.
type Config struct {
	// Default is how new sessions run: "sandbox" (the default) or "host".
	Default string `json:"default,omitempty"`
	// Backend names the Backend sandboxes run in; "" is DefaultBackend.
	Backend string `json:"backend,omitempty"`
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
	// NoEgressProxy turns off network filtering: every connection is allowed. A container's still leave through the
	// egress sidecar, which has the host dial them, so the host's VPN and tailnet stay reachable.
	NoEgressProxy bool `json:"noEgressProxy,omitempty"`
	// Memory caps each container's memory ("8g"; "none" for no cap).
	Memory string `json:"memory,omitempty"`
	// PidsLimit caps each container's processes (-1 for no cap).
	PidsLimit int `json:"pidsLimit,omitempty"`
	// GoCacheMax caps the sandboxes' Go build cache, in docker's units ("20g"); "" is DefaultGoCacheMax, "none" no cap.
	GoCacheMax string `json:"goCacheMax,omitempty"`
	// Loopback is which of the host's loopback ports a sandbox that shares the host's network may connect to:
	// LoopbackAll (""), LoopbackEphemeral or LoopbackNone.
	Loopback string `json:"loopback,omitempty"`
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

// Which loopback ports a sandbox on the host's network may connect to: any; only the ephemeral range, where a
// server on port 0 lands, not services on fixed ports; or none, beyond the sandbox's own proxy.
const (
	LoopbackAll       = "all"
	LoopbackEphemeral = "ephemeral"
	LoopbackNone      = "none"
)

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

// NewProjectConfig is a repository's sandbox settings from their string forms (its sandbox.egress, sandbox.rules
// and sandbox.image), and whether it sets anything. Rules that don't parse are left out.
func NewProjectConfig(egress, rules []string, image string) (ProjectConfig, bool) {
	p := ProjectConfig{Egress: EgressRules(egress), Rules: PolicyRules(rules), Image: image}
	return p, p.Egress != nil || p.Rules != nil || p.Image != ""
}

// EgressRules reads each network rule that parses.
func EgressRules(ss []string) []EgressRule {
	var out []EgressRule
	for _, s := range ss {
		if r, err := ParseEgressRule(s); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// PolicyRules reads each gateway rule that parses.
func PolicyRules(ss []string) []PolicyRule {
	var out []PolicyRule
	for _, s := range ss {
		if r, err := ParsePolicyRule(s); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// mcpServer is a server in Claude's mcpServers form.
type mcpServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ParseServer reads an MCP server written as an http(s) URL (the HTTP transport) or a command line (stdio):
// "npx -y @foo/mcp", with NAME=value words before the command for its environment.
func ParseServer(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		if strings.ContainsAny(s, " \t") {
			return nil, fmt.Errorf("%q: a URL has no spaces", s)
		}
		return json.Marshal(mcpServer{Type: "http", URL: s})
	}
	if strings.Contains(s, "://") {
		return nil, fmt.Errorf("%q: a server is an http(s) URL or a command line", s)
	}
	words, err := splitWords(s)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", s, err)
	}
	var srv mcpServer
	for len(words) > 0 && envAssign.MatchString(words[0]) {
		k, v, _ := strings.Cut(words[0], "=")
		if srv.Env == nil {
			srv.Env = map[string]string{}
		}
		srv.Env[k] = v
		words = words[1:]
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("%q: a server is an http(s) URL or a command line", s)
	}
	srv.Command, srv.Args = words[0], words[1:]
	if len(srv.Args) == 0 {
		srv.Args = nil
	}
	return json.Marshal(srv)
}

// splitWords splits a command line as a shell would, with its quotes and backslashes, without expanding anything.
func splitWords(s string) ([]string, error) {
	var out []string
	var w strings.Builder
	in, quote, escaped := false, rune(0), false
	for _, r := range s {
		switch {
		case escaped:
			w.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, in = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				w.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, w.String())
				w.Reset()
				in = false
			}
		default:
			w.WriteRune(r)
			in = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("an unfinished quote")
	}
	if in {
		out = append(out, w.String())
	}
	return out, nil
}
