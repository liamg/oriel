package kit

import (
	"context"
	"fmt"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

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
