package app

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/worker"
)

var memorySize = regexp.MustCompile(`^[0-9]+[bkmgBKMG]?$`)

// listOf validates each item of a list option with parse.
func listOf[T any](parse func(string) (T, error)) func(any) error {
	return func(v any) error {
		for _, s := range v.([]string) {
			if _, err := parse(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// sandboxOptions are the sandbox's settings (settings.md, "sandbox.*"). A project's file may add its own network and
// gateway rules and image; the rest are yours alone.
func sandboxOptions() []registry.OptionSpec {
	user, project := registry.ScopeUser, registry.ScopeUser|registry.ScopeProject
	return []registry.OptionSpec{
		{Name: "sandbox.default", Type: registry.Enum, Values: []string{"host", "sandbox"}, Default: "host", Scopes: user,
			Help: "Where a new session's agent runs: on the worker (host) or in a container (sandbox). The launcher's Sandbox toggle starts here."},
		{Name: "sandbox.image", Type: registry.String, Default: "", Scopes: project,
			Help: "The image sandboxes run; empty builds Oriel's base image. A project's applies to its repo."},
		{Name: "sandbox.claude_version", Type: registry.String, Default: "", Scopes: user,
			Help: "The Claude Code version a sandbox runs; empty runs the host's."},
		{Name: "sandbox.env", Type: registry.List, Default: []string{}, Scopes: user,
			Help: "The host's environment variables a sandbox inherits, by name; empty means the defaults."},
		{Name: "sandbox.egress", Type: registry.List, Default: []string{}, Scopes: project, Validate: listOf(sandbox.ParseEgressRule),
			Help: `Network rules over the preset allowlist, each "allow|deny host[:port] [private]": "allow *.golang.org:443". A project's apply to its sessions.`},
		{Name: "sandbox.rules", Type: registry.List, Default: []string{}, Scopes: project, Validate: listOf(sandbox.ParsePolicyRule),
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
		{Name: "sandbox.hidden_servers", Type: registry.List, Default: []string{}, Scopes: user,
			Help: "Upstream MCP servers a sandbox never sees, by name."},
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
	}
}

// sandboxConfig is the sandbox.* options as the worker's sandbox.Config: yours, and each project's own at its root.
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
	cfg := sandbox.Config{Default: str("sandbox.default"), Image: str("sandbox.image"), ClaudeVersion: str("sandbox.claude_version"),
		Env: list("sandbox.env"), Hidden: list("sandbox.hidden_servers"), Memory: str("sandbox.memory")}
	cfg.Egress = egressRules(list("sandbox.egress"))
	cfg.Rules = policyRules(list("sandbox.rules"))
	cfg.AutoAllowReadOnly, _ = get("sandbox.auto_allow_read_only").(bool)
	if on, _ := get("sandbox.egress_proxy").(bool); !on {
		cfg.NoEgressProxy = true
	}
	d, _ := get("sandbox.approval_timeout").(time.Duration)
	cfg.ApprovalSecs = int(d / time.Second)
	cfg.PidsLimit, _ = get("sandbox.pids_limit").(int)
	for _, root := range roots {
		at := registry.At{Project: root}
		var p sandbox.ProjectConfig
		if v, ok := o.Lookup("sandbox.egress", registry.LayerProject, at); ok {
			p.Egress = egressRules(v.([]string))
		}
		if v, ok := o.Lookup("sandbox.rules", registry.LayerProject, at); ok {
			p.Rules = policyRules(v.([]string))
		}
		if v, ok := o.Lookup("sandbox.image", registry.LayerProject, at); ok {
			p.Image = v.(string)
		}
		if p.Egress == nil && p.Rules == nil && p.Image == "" {
			continue
		}
		if cfg.Projects == nil {
			cfg.Projects = map[string]sandbox.ProjectConfig{}
		}
		cfg.Projects[root] = p
	}
	return cfg
}

func egressRules(ss []string) []sandbox.EgressRule {
	var out []sandbox.EgressRule
	for _, s := range ss {
		if r, err := sandbox.ParseEgressRule(s); err == nil {
			out = append(out, r)
		}
	}
	return out
}

func policyRules(ss []string) []sandbox.PolicyRule {
	var out []sandbox.PolicyRule
	for _, s := range ss {
		if r, err := sandbox.ParsePolicyRule(s); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// projectRoots are the checkouts whose project settings apply here: the window's project, and the repos of every
// session on this machine's worker.
func projectRoots(workDir string, st core.State) []string {
	var out []string
	add := func(p string) {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	add(workDir)
	for _, s := range st.Sessions {
		if s.Worker != "" && s.Worker != core.LocalWorker {
			continue
		}
		for _, p := range s.Projects {
			add(p.Repo)
		}
	}
	return out
}

// sandboxSettings sends the sandbox.* options to every worker: when one connects, and whenever one changes.
func (x *sessions) sandboxSettings(ctx context.Context) {
	o := x.h.Options()
	for _, sp := range sandboxOptions() {
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
			for _, c := range x.clients() {
				x.pushSandbox(ctx, c, cfg)
			}
		}
	}()
}

// sendSandbox asks for the settings to go to the workers; asks that come while they're going are one.
func (x *sessions) sendSandbox() {
	select {
	case x.sandboxKick <- struct{}{}:
	default:
	}
}

func (x *sessions) pushSandbox(ctx context.Context, c worker.Client, cfg sandbox.Config) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.SetSandboxConfig(ctx, cfg); err != nil {
		x.h.Log().Warn("sandbox settings", "err", err)
	}
}
