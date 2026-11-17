package kit

import (
	"cmp"
	"context"
	"fmt"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// SessionStart is the SessionStart hook's event: a session just started, from the launcher, a command or a script.
type SessionStart struct {
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Ticket  string `json:"ticket,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// specText are session.start's text args, each with the spec's field: SpecFrom and SpecArgs both read them, so a key
// is spelled once.
var specText = []struct {
	key   string
	field func(*core.SessionSpec) *string
}{
	{"name", func(s *core.SessionSpec) *string { return &s.Name }},
	{"prompt", func(s *core.SessionSpec) *string { return &s.Prompt }},
	{"ticket", func(s *core.SessionSpec) *string { return &s.Ticket }},
	{"context", func(s *core.SessionSpec) *string { return &s.Context }},
	{"pr", func(s *core.SessionSpec) *string { return &s.PR }},
	{"pr_remote", func(s *core.SessionSpec) *string { return &s.PRRemote }},
	{"pr_ref", func(s *core.SessionSpec) *string { return &s.PRRef }},
	{"dir", func(s *core.SessionSpec) *string { return &s.Dir }},
	{"branch", func(s *core.SessionSpec) *string { return &s.Branch }},
	{"base", func(s *core.SessionSpec) *string { return &s.Base }},
	{"provider", func(s *core.SessionSpec) *string { return &s.Provider }},
	{"model", func(s *core.SessionSpec) *string { return &s.Model }},
	{"effort", func(s *core.SessionSpec) *string { return &s.Effort }},
	{"template", func(s *core.SessionSpec) *string { return &s.Template }},
	{"network", func(s *core.SessionSpec) *string { return &s.Sandbox.Network }},
	{"gate", func(s *core.SessionSpec) *string { return &s.Sandbox.Gate }},
	{"backend", func(s *core.SessionSpec) *string { return &s.Sandbox.Backend }},
}

// SpecFrom is a session spec from session.start's args.
func SpecFrom(a registry.Args) core.SessionSpec {
	s := core.SessionSpec{Kind: core.Kind(a.String("kind")),
		Sandbox: core.SandboxPolicy{Container: a.Bool("sandbox"), CustomImage: a.Bool("custom_image")}}
	for _, t := range specText {
		*t.field(&s) = a.String(t.key)
	}
	if s.Kind == "" {
		s.Kind = core.KindWork
	}
	for _, r := range a.Strings("repos") {
		s.Repos = append(s.Repos, core.RepoID(r))
	}
	return s
}

// SpecArgs are session.start's args for s, SpecFrom's inverse: what's empty is left out, and a sandbox with no network
// set asks for each new host.
func SpecArgs(s core.SessionSpec) registry.Args {
	a := registry.Args{"kind": string(cmp.Or(s.Kind, core.KindWork)), "sandbox": s.Sandbox.Container}
	if s.Sandbox.Container && s.Sandbox.Network == "" {
		s.Sandbox.Network = "ask"
	}
	for _, t := range specText {
		if v := *t.field(&s); v != "" {
			a[t.key] = v
		}
	}
	if s.Sandbox.CustomImage {
		a["custom_image"] = true
	}
	if len(s.Repos) > 0 {
		repos := make([]string, len(s.Repos))
		for i, r := range s.Repos {
			repos[i] = string(r)
		}
		a["repos"] = repos
	}
	return a
}

// DefaultSandbox is what sandbox.default gives a new session: a sandbox asking for each new host, or none.
func DefaultSandbox(o registry.Options) core.SandboxPolicy {
	if v, _ := registry.Option[string](o, "sandbox.default", registry.At{}); v == "sandbox" {
		return core.SandboxPolicy{Container: true, Network: "ask"}
	}
	return core.SandboxPolicy{}
}

// Call is a command to run, with its args: one step of a then, what a command runs once it's done.
type Call struct {
	Command string
	Args    registry.Args
}

// Then is calls as a command's then argument travels, through Lua and JSON as well as Go: a list of
// {command, args}, which CallsOf reads back.
func Then(calls ...Call) []any {
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		m := map[string]any{"command": c.Command}
		if c.Args != nil {
			m["args"] = map[string]any(c.Args)
		}
		out = append(out, m)
	}
	return out
}

// CallsOf reads a then: a list of {command, args} (Then), or the calls themselves.
func CallsOf(v any) ([]Call, error) {
	if v == nil {
		return nil, nil
	}
	if calls, ok := v.([]Call); ok {
		return calls, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("then is a list of {command, args}, not %T", v)
	}
	var out []Call
	for _, it := range list {
		m, ok := asArgs(it)
		if !ok || m.String("command") == "" {
			return nil, fmt.Errorf("then's %v isn't a {command, args}", it)
		}
		args, _ := asArgs(m["args"])
		out = append(out, Call{Command: m.String("command"), Args: args})
	}
	return out, nil
}

func asArgs(v any) (registry.Args, bool) {
	switch v := v.(type) {
	case registry.Args:
		return v, true
	case map[string]any:
		return registry.Args(v), true
	}
	return nil, false
}

// RunCalls runs calls in order, logging what fails.
func RunCalls(h plugin.Host, calls []Call) {
	for _, c := range calls {
		if err := h.Run(context.Background(), c.Command, c.Args); err != nil {
			h.Log().Warn("session.open then", "command", c.Command, "err", err)
		}
	}
}
