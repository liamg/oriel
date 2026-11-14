package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
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

// sandboxOptions are the sandbox's settings (docs/configuration.md). A project's file may add its own network and
// gateway rules and image; the rest are yours alone. o is where sandbox.servers is read, and r's agent providers
// are asked, for the servers a sandbox could hide.
func sandboxOptions(o registry.Options, r registry.Contributions, available func() []string) []registry.OptionSpec {
	offered := offeredBackends(available)
	user, project := registry.ScopeUser, registry.ScopeUser|registry.ScopeProject
	return []registry.OptionSpec{
		{Name: "sandbox.default", Type: registry.Enum, Values: []string{"host", "sandbox"}, Default: "sandbox", Scopes: user,
			Help: "Where a new session's agent runs: on this machine (host) or in a container (sandbox). The launcher's Sandbox toggle starts here."},
		{Name: "sandbox.image", Type: registry.String, Default: baseImage, Scopes: project,
			Help: "The image sandboxes run: base builds Oriel's base image. A project's applies to its repo."},
		{Name: "sandbox.env", Type: registry.List, Default: []string{}, Scopes: user, Choices: envNames,
			Help: "The host's environment variables a sandbox inherits, by name; empty means the defaults."},
		{Name: "sandbox.egress", Type: registry.List, Default: []string{}, Scopes: project, Validate: parsed(sandbox.ParseEgressRule),
			Help: `Network rules over the preset allowlist, each "allow|deny host[:port] [private]": "allow *.golang.org:443". A project's apply to its sessions.`},
		{Name: "sandbox.rules", Type: registry.List, Default: []string{}, Scopes: project, Validate: parsed(sandbox.ParsePolicyRule),
			Help: `Gateway rules, each "allow|ask|deny tool", the tool a name or a glob: "deny oriel__push". A project's apply to its sessions.`},
		{Name: "sandbox.approval_timeout", Type: registry.Duration, Default: 10 * time.Minute, Scopes: user,
			Validate: func(v any) error {
				if v.(time.Duration) <= 0 {
					return errors.New("a wait must be longer than nothing")
				}
				return nil
			},
			Help: "How long a held gateway call waits for you."},
		{Name: "sandbox.auto_allow_read_only", Type: registry.Bool, Default: false, Scopes: user,
			Help: "Allow the upstream MCP tools that say they only read, without asking."},
		{Name: "sandbox.hidden_servers", Type: registry.List, Default: []string{}, Scopes: user, Choices: func() []string { return upstreamNames(o, r) },
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
			Help: "Filter a container's network through the egress proxy; off allows every connection."},
		{Name: "sandbox.memory", Type: registry.String, Default: defaultMemory, Scopes: user,
			Validate: func(v any) error {
				if s := v.(string); s != "none" && !memorySize.MatchString(s) {
					return errors.New(`a size in docker's units ("8g"), or "none"`)
				}
				return nil
			},
			Help: `Each container's memory cap in docker's units ("8g"), or "none" for no cap.`},
		{Name: "sandbox.pids_limit", Type: registry.Int, Default: defaultPids, Min: -1, Max: 1 << 20, Scopes: user,
			Validate: func(v any) error {
				if v.(int) == 0 {
					return errors.New("a cap of 0 processes runs nothing: -1 for no cap")
				}
				return nil
			},
			Help: "Each container's process cap; -1 for no cap."},
		{Name: "sandbox.go_cache_max", Type: registry.String, Default: defaultGoCache, Scopes: user,
			Validate: func(v any) error {
				if s := v.(string); s != "none" && !memorySize.MatchString(s) {
					return errors.New(`a size in docker's units ("20g"), or "none"`)
				}
				return nil
			},
			Help: `The cap on the sandboxes' shared Go build cache ("20g"): the hourly sweep removes the least recently used past it; "none" for no cap.`},
		{Name: "sandbox.backend", Type: registry.Enum, Values: offered, Default: offered[0], Scopes: user,
			Help: "What confines the agent: a container engine; or seatbelt, macOS's own sandbox."},
		{Name: "sandbox.loopback", Type: registry.Enum, Values: []string{sandbox.LoopbackAll, sandbox.LoopbackEphemeral, sandbox.LoopbackNone},
			Default: sandbox.LoopbackAll, Scopes: user,
			Help: "The host's loopback ports a seatbelt sandbox may connect to: all; ephemeral, not services on fixed ports; or none."},
	}
}

// The sandbox's own defaults, as the settings show them.
const (
	baseImage      = "base"
	defaultMemory  = container.DefaultMemory
	defaultGoCache = "20g" // sandbox.DefaultGoCacheMax
	defaultPids    = container.DefaultPidsLimit
)

// offeredBackends are the sandbox backends to offer (available), the default first; docker when there are none.
func offeredBackends(available func() []string) []string {
	if available != nil {
		if a := available(); len(a) > 0 {
			return a
		}
	}
	return []string{"docker"}
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

// upstreamNames are the MCP servers every sandbox's gateway finds on this machine (gateway.Found): sandbox.servers',
// and those the user installed for each agent provider.
func upstreamNames(o registry.Options, r registry.Contributions) []string {
	m, _, _ := o.Get("sandbox.servers", registry.At{})
	servers, _ := mcpServers(m)
	var providers []agent.Provider
	for _, p := range services.AgentProviders(r) {
		providers = append(providers, p.Provider)
	}
	var out []string
	for _, s := range gateway.Found(providers, servers, nil) {
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
	// The settings' defaults are the sandbox's own, which it takes as nothing set: seatbelt warns only of what is.
	cfg := sandbox.Config{Default: str("sandbox.default"), Backend: str("sandbox.backend"), Image: kit.Unless(str("sandbox.image"), baseImage),
		Env: list("sandbox.env"), Hidden: list("sandbox.hidden_servers"), Memory: kit.Unless(str("sandbox.memory"), defaultMemory),
		GoCacheMax: kit.Unless(str("sandbox.go_cache_max"), defaultGoCache), Loopback: str("sandbox.loopback")}
	cfg.Egress = sandbox.EgressRules(list("sandbox.egress"))
	cfg.Rules = sandbox.PolicyRules(list("sandbox.rules"))
	cfg.Servers, _ = mcpServers(get("sandbox.servers"))
	cfg.AutoAllowReadOnly, _ = get("sandbox.auto_allow_read_only").(bool)
	if on, _ := get("sandbox.egress_proxy").(bool); !on {
		cfg.NoEgressProxy = true
	}
	d, _ := get("sandbox.approval_timeout").(time.Duration)
	cfg.ApprovalSecs = int(d / time.Second)
	if n, _ := get("sandbox.pids_limit").(int); n != defaultPids {
		cfg.PidsLimit = n
	}
	for _, root := range roots {
		at := registry.At{Project: root}
		egress, _ := o.Lookup("sandbox.egress", registry.LayerProject, at)
		rules, _ := o.Lookup("sandbox.rules", registry.LayerProject, at)
		image, _ := o.Lookup("sandbox.image", registry.LayerProject, at)
		e, _ := egress.([]string)
		r, _ := rules.([]string)
		i, _ := image.(string)
		i = kit.Unless(i, baseImage)
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

// SandboxSettings declares the sandbox's settings, and sends them to the session host: at start, and whenever one
// changes.
type SandboxSettings struct {
	kit.Disposer
	// Backends are the sandbox backends to offer, the default first; docker alone if nil.
	Backends func() []string
	h        plugin.Host
	sends    work.Serial // the settings, going to the host
	queued   atomic.Bool // a send is queued and hasn't started
}

func (x *SandboxSettings) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sandbox.settings"} }

func (x *SandboxSettings) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	o := h.Options()
	for _, sp := range sandboxOptions(o, h, x.Backends) {
		x.Add(h.Option(sp))
		x.Add(o.Watch(sp.Name, x.send))
	}
	x.Add(registry.DisposeFunc(x.sends.Stop))
	x.Add(h.Contribute(registry.Busy{Name: "sandbox.settings", Busy: x.sends.Busy, Source: "builtin:sandbox.settings"}))
	if kit.HasSessions(h) {
		x.send()
		x.Add(kit.OnDirChanged(h, "the sandbox's rules for the directory's project", func(kit.DirChanged) { x.send() }))
	}
	return nil
}

// send asks for the settings to go to the host; asks that come before they go are one.
func (x *SandboxSettings) send() {
	if !x.queued.CompareAndSwap(false, true) {
		return
	}
	x.sends.Queue(func() {
		x.queued.Store(false)
		h := x.h
		cfg := sandboxConfig(h.Options(), projectRoots(kit.WindowDir(h), h.State())) // the window's project's file may add rules
		if w, err := kit.SandboxCalls(h); err == nil {
			if err := w.SetSandboxConfig(cfg); err != nil {
				h.Log().Warn("sandbox settings", "err", err)
			}
		}
	})
}
