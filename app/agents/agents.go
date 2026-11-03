// Package agents is the built-in agent providers, Claude and Codex, each a plugin that registers its provider
// (registry.AgentProvider) as any plugin's would.
package agents

import (
	"context"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/codex"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// VersionOption pins the Claude Code a container sandbox runs.
const VersionOption = "sandbox.claude_version"

// Claude is Claude Code, run as its interactive CLI in a PTY with Oriel's mod.
type Claude struct {
	kit.Disposer
	ProbeDir string         // where the /usage probe runs claude; one in the temp dir if ""
	Provider agent.Provider // what it registers; a claude.Provider if nil
}

func (x *Claude) Manifest() plugin.Manifest { return plugin.Manifest{ID: "claude"} }

func (x *Claude) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Option(registry.OptionSpec{Name: VersionOption, Type: registry.String, Default: "", Scopes: registry.ScopeUser,
		Help: "The Claude Code version a container sandbox runs; empty runs the host's."}))
	p := x.Provider
	if p == nil {
		o := h.Options()
		p = &claude.Provider{ProbeDir: x.ProbeDir, SandboxVersion: func() string {
			v, _, _ := o.Get(VersionOption, registry.At{})
			s, _ := v.(string)
			return s
		}}
	}
	x.Add(h.Contribute(registry.AgentProvider{Name: "claude", Title: "Claude", Mark: "C", Tone: "usage.weekly", Order: 10,
		Provider: p, Source: "builtin:claude"}))
	return nil
}

// Codex is OpenAI's Codex, run as `codex app-server`.
type Codex struct {
	kit.Disposer
	Provider agent.Provider // what it registers; a codex.Provider if nil
}

func (x *Codex) Manifest() plugin.Manifest { return plugin.Manifest{ID: "codex"} }

func (x *Codex) Activate(_ context.Context, h plugin.Host) error {
	p := x.Provider
	if p == nil {
		p = &codex.Provider{}
	}
	x.Add(h.Contribute(registry.AgentProvider{Name: "codex", Title: "Codex", Mark: "CX", Order: 20, Provider: p, Source: "builtin:codex"}))
	return nil
}

// Builtins are the built-in providers' plugins; probeDir is where Claude's /usage probe runs.
func Builtins(probeDir string) []plugin.Plugin {
	return []plugin.Plugin{&Claude{ProbeDir: probeDir}, &Codex{}}
}

// For is the built-in plugin of p's ID registering p in its place: how a test stands a fake in for Claude.
func For(p agent.Provider) (plugin.Plugin, bool) {
	switch p.ID() {
	case "claude":
		return &Claude{Provider: p}, true
	case "codex":
		return &Codex{Provider: p}, true
	}
	return nil, false
}
