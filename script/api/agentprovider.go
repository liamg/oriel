package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/rpcagent"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// providerKeys are what oriel.agent.provider's spec says itself; a table under any other key configures the bridge
// of that name.
var providerKeys = []string{"name", "title", "mark", "tone", "order", "program", "args", "env", "features", "logs", "discover"}

//oriel:api doc="An agent sessions can run, as the launcher offers Claude and Codex, shadowing a built-in of the same name (name = 'claude' replaces Claude): { name, title, mark (its square's letters), tone (a theme token), order (lower is offered first; Claude is 10), features = { terminal, fork, images, sandbox, login, stop_processes, notes = 'AGENTS.md' } (what it does before its process has said, in initialize), logs, discover (it answers those calls) }, and how it's served: program = 'bin/my-agent' with args and env = { NAME = value }, a process speaking the Oriel agent protocol (docs/agent-protocol.md) that starts on first use, OS-sandboxed by the plugin's grants as any process plugin is; or a bridge's config under the bridge's name: acp = { command = { 'gemini', '--experimental-acp' }, env, models = { { id, name, default } }, login = 'gemini', hosts = { 'generativelanguage.googleapis.com' }, tools = { 'node@22', 'npm:@google/gemini-cli@0.9.0' }, pass_env = { 'GEMINI_*' }, home = { '.gemini' } } runs an Agent Client Protocol agent through the bundled ACP bridge; hosts, tools, pass_env and home are what it needs in a sandbox: the hosts it reaches, the mise tools a container installs to run it (macOS's sandbox runs your installed program), the host variables it inherits, and the entries of your home it keeps its sign-in in, which it sees, read-write, at the sandbox's home. What the agent runs outside a session's sandbox needs the plugin's exec grant for it: the ACP agent's command too." cap=agent.provider
func (g Agent) Provider(spec script.Table, where string) error {
	a := g.a
	name := str(spec, "name")
	if name == "" {
		return errors.New("an agent provider needs a name")
	}
	o := rpcagent.Options{Name: name, Allow: func(path string) error { return a.Plugin.Check(plugin.CapExec, plugin.RealPath(path)) }}
	o.Logged, _ = spec["logs"].(bool)
	o.Discovered, _ = spec["discover"].(bool)
	if f, ok := spec["features"].(script.Table); ok {
		b := func(k string) bool { v, _ := f[k].(bool); return v }
		o.Assume = agent.Features{Terminal: b("terminal"), Fork: b("fork"), Images: b("images"), Sandbox: b("sandbox"), Login: b("login"),
			StopProcesses: b("stop_processes"), Notes: str(f, "notes")}
	}
	var bridge string
	var config any
	for k, v := range spec {
		if slices.Contains(providerKeys, k) {
			continue
		}
		if _, ok := v.(script.Table); !ok {
			continue
		}
		if bridge != "" {
			return fmt.Errorf("agent provider %s names two bridges: %s and %s", name, bridge, k)
		}
		bridge, config = k, v
	}
	program := str(spec, "program")
	switch {
	case program != "" && bridge != "":
		return fmt.Errorf("agent provider %s has both a program and the %s bridge", name, bridge)
	case program != "":
		o.Dial = a.agentDialer(program, strs(spec["args"]), envOf(spec["env"]))
	case bridge != "":
		raw, err := json.Marshal(emptyNil(config))
		if err != nil {
			return fmt.Errorf("agent provider %s's %s config: %w", name, bridge, err)
		}
		o.Config = raw
		o.Dial = func(ctx context.Context, h rpcagent.Handler) (rpcagent.Peer, error) {
			b, ok := rpcagent.BridgeOf(a.Host, bridge)
			if !ok {
				return nil, fmt.Errorf("agent provider %s: no agent bridge %q is registered (is its plugin turned off?)", name, bridge)
			}
			return b.Dial(ctx, h)
		}
	default:
		return fmt.Errorf("agent provider %s needs a program, or a bridge's config (acp = { command = … })", name)
	}
	p := rpcagent.New(o)
	ap := registry.AgentProvider{Name: name, Title: str(spec, "title"), Mark: str(spec, "mark"), Tone: str(spec, "tone"), Order: 50,
		Provider: p, Source: where}
	if n, ok := spec["order"].(float64); ok {
		ap.Order = int(n)
	}
	a.own(a.Host.Contribute(ap))
	if c, ok := p.(interface{ Close() error }); ok {
		a.own(disposeFunc(func() { c.Close() }))
	}
	return nil
}

//oriel:api doc="A bridge: the plugin's program serving the Oriel agent protocol (docs/agent-protocol.md) for the agents other plugins configure with it, as the bundled ACP bridge serves any Agent Client Protocol agent: { name, title, program = 'bin/my-bridge', args, env = { NAME = value } }. A provider whose spec has a table under the bridge's name (oriel.agent.provider{ name = 'gemini', acp = { … } }) gets a process of the bridge's, handed that table in initialize's config; what it runs is held to the provider's plugin's grants." cap=agent.provider
func (g Agent) Bridge(spec script.Table, where string) error {
	a := g.a
	name, program := str(spec, "name"), str(spec, "program")
	if name == "" || program == "" {
		return errors.New("an agent bridge needs a name and a program")
	}
	a.own(a.Host.Contribute(rpcagent.Bridge{Name: name, Title: str(spec, "title"), Source: where,
		Dial: a.agentDialer(program, strs(spec["args"]), envOf(spec["env"]))}))
	return nil
}

// agentDialer starts the plugin's program as a process plugin speaking the agent protocol.
func (a *APIs) agentDialer(program string, args, env []string) rpcagent.Dialer {
	return func(_ context.Context, h rpcagent.Handler) (rpcagent.Peer, error) {
		proc, err := a.processes().Spawn(a.Plugin, program, plugin.SpawnOpts{Args: args, Env: env, Cancel: rpcagent.CancelRequest(),
			Request: func(method string, params json.RawMessage) (any, bool, error) {
				v, err := h.Request(context.Background(), method, params)
				return v, true, err
			},
			Notify: func(method string, params json.RawMessage) (any, bool, error) {
				h.Notify(method, params)
				return nil, true, nil
			}})
		if err != nil {
			return nil, err
		}
		return processPeer{proc}, nil
	}
}

type processPeer struct{ *plugin.Process }

func (p processPeer) Close() error { return p.Stop() }

func envOf(v any) []string {
	var env []string
	for k, x := range tableOf(v) {
		env = append(env, k+"="+fmt.Sprint(x))
	}
	sort.Strings(env)
	return env
}
