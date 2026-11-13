package kit

import (
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

// SpecFrom is a session spec from a command's args.
func SpecFrom(a registry.Args) core.SessionSpec {
	s := core.SessionSpec{Kind: core.Kind(a.String("kind")), Name: a.String("name"), Prompt: a.String("prompt"), Ticket: a.String("ticket"), Context: a.String("context"),
		PR: a.String("pr"), PRRemote: a.String("pr_remote"), PRRef: a.String("pr_ref"), Dir: a.String("dir"), Branch: a.String("branch"), Base: a.String("base"), Provider: a.String("provider"),
		Model: a.String("model"), Effort: a.String("effort"), Template: a.String("template"),
		Sandbox: core.SandboxPolicy{Container: a.Bool("sandbox"), Network: a.String("network"), Gate: a.String("gate"),
			Backend: a.String("backend")}}
	if s.Kind == "" {
		s.Kind = core.KindWork
	}
	for _, r := range a.Strings("repos") {
		s.Repos = append(s.Repos, core.RepoID(r))
	}
	return s
}

// open brings a session to the front, resuming an archived one.
// Call is a command to run, with its args.
type Call struct {
	Command string
	Args    registry.Args
}

// CallsOf reads a list of {command, args}.
func CallsOf(v any) ([]Call, error) {
	if v == nil {
		return nil, nil
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
