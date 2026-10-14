package registry

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// AgentTools are the agent tools (contrib.go), by name.
func AgentTools(r Registry) []AgentTool {
	var out []AgentTool
	for _, c := range r.Contributions("agent.tool") {
		out = append(out, c.(AgentTool))
	}
	slices.SortFunc(out, func(a, b AgentTool) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Schema is the tool's input as JSON Schema, from Params: a type ending in "?" is optional.
func (t AgentTool) Schema() json.RawMessage {
	props := map[string]any{}
	var required []string
	for name, typ := range t.Params {
		opt := strings.HasSuffix(typ, "?")
		typ = strings.TrimSuffix(typ, "?")
		if typ == "" {
			typ = "string"
		}
		props[name] = map[string]string{"type": typ}
		if !opt {
			required = append(required, name)
		}
	}
	slices.Sort(required)
	if required == nil {
		required = []string{}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required})
	return b
}

type sessionKey struct{}

// WithSession is ctx for a call made for a session: an agent tool's Run reads it with SessionOf.
func WithSession(ctx Ctx, id string) Ctx { return context.WithValue(ctx, sessionKey{}, id) }

// SessionOf is the session a call is for, or "".
func SessionOf(ctx Ctx) string {
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}

// PermissionEvent is what the "Permission" hooks see before an ask reaches the user (agents.md → Permissions,
// layer 2). A Decide hook answers with "allow" or "deny", or a PermissionVerdict to give the agent a reason.
type PermissionEvent struct {
	Session string `json:"session"`
	Kind    string `json:"kind"` // permission, network, gateway
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary"`
	Input   string `json:"input,omitempty"` // a shell's command, an edit's path, else the tool's input as JSON
	Rule    string `json:"rule,omitempty"`  // what "Always" would allow
	Host    string `json:"host,omitempty"`  // network: host:port
}

// PermissionVerdict is a Decide hook's answer with a note the agent reads.
type PermissionVerdict struct {
	Decision string `json:"decision"` // allow or deny
	Note     string `json:"note,omitempty"`
}
