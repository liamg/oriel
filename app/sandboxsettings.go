package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/gateway"
)

var (
	memorySize = regexp.MustCompile(`^[0-9]+[bkmgBKMG]?$`)
	serverName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// parsed validates each item of a list option with parse.
func parsed[T any](parse func(string) (T, error)) func(any) error {
	return registry.ListOf(func(s string) error { _, err := parse(s); return err })
}

// sandboxOptions are the sandbox's settings (settings.md, "sandbox.*"). A project's file may add its own network and
// gateway rules and image; the rest are yours alone. o is where sandbox.servers is read for the servers a sandbox
// could hide.
func sandboxOptions(o registry.Options) []registry.OptionSpec {
	user, project := registry.ScopeUser, registry.ScopeUser|registry.ScopeProject
	return []registry.OptionSpec{
		{Name: "sandbox.default", Type: registry.Enum, Values: []string{"host", "sandbox"}, Default: "sandbox", Scopes: user,
			Help: "Where a new session's agent runs: on this machine (host) or in a container (sandbox). The launcher's Sandbox toggle starts here."},
		{Name: "sandbox.image", Type: registry.String, Default: "", Scopes: project,
			Help: "The image sandboxes run; empty builds Oriel's base image. A project's applies to its repo."},
		{Name: "sandbox.claude_version", Type: registry.String, Default: "", Scopes: user,
			Help: "The Claude Code version a sandbox runs; empty runs the host's."},
		{Name: "sandbox.env", Type: registry.List, Default: []string{}, Scopes: user, Choices: envNames,
			Help: "The host's environment variables a sandbox inherits, by name; empty means the defaults."},
		{Name: "sandbox.egress", Type: registry.List, Default: []string{}, Scopes: project, Validate: parsed(sandbox.ParseEgressRule),
			Help: `Network rules over the preset allowlist, each "allow|deny host[:port] [private]": "allow *.golang.org:443". A project's apply to its sessions.`},
		{Name: "sandbox.rules", Type: registry.List, Default: []string{}, Scopes: project, Validate: parsed(sandbox.ParsePolicyRule),
			Help: `Gateway rules, each "allow|ask|deny tool", the tool a name or a glob: "deny oriel__push". A project's apply to its sessions.`},
		{Name: "sandbox.approval_timeout", Type: registry.Duration, Default: time.Duration(0), Scopes: user,
			Validate: func(v any) error {
				if v.(time.Duration) < 0 {
					return errors.New("a wait can't be negative")
				}
				return nil
			},
			Help: "How long a held gateway call waits for you; 0 is ten minutes."},
		{Name: "sandbox.auto_allow_read_only", Type: registry.Bool, Default: false, Scopes: user,
			Help: "Allow the upstream MCP tools that say they only read, without asking."},
		{Name: "sandbox.hidden_servers", Type: registry.List, Default: []string{}, Scopes: user, Choices: func() []string { return upstreamNames(o) },
			Help: "Upstream MCP servers a sandbox never sees, by name."},
		{Name: "sandbox.servers", Type: registry.Map, Default: map[string]string{}, Scopes: user, KeyLabel: "name", ValueLabel: "server",
			Validate: registry.MapOf(func(name, server string) error {
				if !serverName.MatchString(name) {
					return fmt.Errorf("%q: a server's name is lower case letters, digits, - and _", name)
				}
				_, err := sandbox.ParseServer(server)
				return err
			}),
			Help: `Extra MCP servers for the gateway, by name: an https URL, or a command line ("npx -y @foo/mcp").`},
		{Name: "sandbox.egress_proxy", Type: registry.Bool, Default: true, Scopes: user,
			Help: "Filter a container's network through the egress proxy; off gives it plain internet."},
		{Name: "sandbox.memory", Type: registry.String, Default: "", Scopes: user,
			Validate: func(v any) error {
				if s := v.(string); s != "" && s != "none" && !memorySize.MatchString(s) {
					return errors.New(`a size in docker's units ("8g"), or "none"`)
				}
				return nil
			},
			Help: `Each container's memory cap in docker's units ("8g"); empty is 8g, "none" no cap.`},
		{Name: "sandbox.pids_limit", Type: registry.Int, Default: 0, Min: -1, Max: 1 << 20, Scopes: user,
			Help: "Each container's process cap; 0 is 4096, -1 no cap."},
		{Name: "sandbox.backend", Type: registry.Enum, Values: sandbox.Backends(), Default: sandbox.DefaultBackend, Scopes: user,
			Help: "What confines the agent: docker, a container; or seatbelt, macOS's own sandbox."},
		{Name: "sandbox.loopback", Type: registry.Enum, Values: []string{sandbox.LoopbackAll, sandbox.LoopbackEphemeral, sandbox.LoopbackNone},
			Default: sandbox.LoopbackAll, Scopes: user,
			Help: "The host's loopback ports a seatbelt sandbox may connect to: all; ephemeral, not services on fixed ports; or none."},
	}
}

// envNames are the host's environment variables, by name.
func envNames() []string {
	var out []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// upstreamNames are the MCP servers the gateway finds on this machine, sandbox.servers' among them.
func upstreamNames(o registry.Options) []string {
	home, _ := os.UserHomeDir()
	m, _, _ := o.Get("sandbox.servers", registry.At{})
	servers, _ := mcpServers(m)
	var out []string
	for _, s := range gateway.Discover(home, servers, nil) {
		out = append(out, s.Name)
	}
	return out
}

// mcpServers are sandbox.servers in Claude's mcpServers form; a server that doesn't parse is left out.
func mcpServers(v any) (map[string]json.RawMessage, bool) {
	m, _ := v.(map[string]string)
	if len(m) == 0 {
		return nil, false
	}
	out := map[string]json.RawMessage{}
	for name, line := range m {
		if raw, err := sandbox.ParseServer(line); err == nil {
			out[name] = raw
		}
	}
	return out, true
}

// sandboxConfig is the sandbox.* options as the host's sandbox.Config: yours, and each project's own at its root.
func sandboxConfig(o registry.Options, roots []string) sandbox.Config {
	get := func(name string) any { v, _, _ := o.Get(name, registry.At{}); return v }
	list := func(name string) []string {
		l, _ := get(name).([]string)
		if len(l) == 0 {
			return nil
		}
		return slices.Clone(l)
	}
	str := func(name string) string { s, _ := get(name).(string); return s }
	cfg := sandbox.Config{Default: str("sandbox.default"), Backend: str("sandbox.backend"), Image: str("sandbox.image"), ClaudeVersion: str("sandbox.claude_version"),
		Env: list("sandbox.env"), Hidden: list("sandbox.hidden_servers"), Memory: str("sandbox.memory"),
		Loopback: str("sandbox.loopback")}
	cfg.Egress = sandbox.EgressRules(list("sandbox.egress"))
	cfg.Rules = sandbox.PolicyRules(list("sandbox.rules"))
	cfg.Servers, _ = mcpServers(get("sandbox.servers"))
	cfg.AutoAllowReadOnly, _ = get("sandbox.auto_allow_read_only").(bool)
	if on, _ := get("sandbox.egress_proxy").(bool); !on {
		cfg.NoEgressProxy = true
	}
	d, _ := get("sandbox.approval_timeout").(time.Duration)
	cfg.ApprovalSecs = int(d / time.Second)
	cfg.PidsLimit, _ = get("sandbox.pids_limit").(int)
	for _, root := range roots {
		at := registry.At{Project: root}
		egress, _ := o.Lookup("sandbox.egress", registry.LayerProject, at)
		rules, _ := o.Lookup("sandbox.rules", registry.LayerProject, at)
		image, _ := o.Lookup("sandbox.image", registry.LayerProject, at)
		e, _ := egress.([]string)
		r, _ := rules.([]string)
		i, _ := image.(string)
		p, ok := sandbox.NewProjectConfig(e, r, i)
		if !ok {
			continue
		}
		if cfg.Projects == nil {
			cfg.Projects = map[string]sandbox.ProjectConfig{}
		}
		cfg.Projects[root] = p
	}
	return cfg
}

// projectRoots are the checkouts whose project settings apply here: the window's project, and the repos of every
// session.
func projectRoots(workDir string, st core.State) []string {
	var out []string
	add := func(p string) {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	add(workDir)
	for _, s := range st.Sessions {
		for _, p := range s.Projects {
			add(p.Repo)
		}
	}
	return out
}

// sandboxSettings sends the sandbox.* options to the session host: at start, and whenever one changes.
func (x *sessions) sandboxSettings(ctx context.Context) {
	o := x.h.Options()
	for _, sp := range sandboxOptions(o) {
		x.add(x.h.Option(sp))
		x.add(onDispose(o.Watch(sp.Name, x.sendSandbox)))
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-x.sandboxKick:
			}
			cfg := sandboxConfig(o, projectRoots(x.sh.cfg.WorkDir, x.sh.cfg.Store.State()))
			if w := x.sh.cfg.Host; w != nil {
				if err := w.SetSandboxConfig(cfg); err != nil {
					x.h.Log().Warn("sandbox settings", "err", err)
				}
			}
		}
	}()
}

// sendSandbox asks for the settings to go to the host; asks that come while they're going are one.
func (x *sessions) sendSandbox() {
	select {
	case x.sandboxKick <- struct{}{}:
	default:
	}
}
