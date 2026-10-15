package registry

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Schema is the tool's input as JSON Schema: InputSchema, else from Params, where a type ending in "?" is optional.
func (t AgentTool) Schema() json.RawMessage {
	if len(t.InputSchema) > 0 {
		return t.InputSchema
	}
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

// OfferedTo says whether the tool is offered to an agent: a session's, named by its kind, or a utility agent.
func (t AgentTool) OfferedTo(agent string, utility bool) bool {
	if len(t.For) == 0 {
		return !utility
	}
	return slices.Contains(t.For, agent)
}

// AgentToolsFor are the agent tools offered to an agent (OfferedTo), by name.
func AgentToolsFor(r Registry, agent string, utility bool) []AgentTool {
	return slices.DeleteFunc(AgentTools(r), func(t AgentTool) bool { return !t.OfferedTo(agent, utility) })
}

type agentKey struct{}

// WithAgent is ctx for a call an agent made: a session's kind, or a utility agent's name.
func WithAgent(ctx Ctx, agent string) Ctx { return context.WithValue(ctx, agentKey{}, agent) }

// AgentOf is the agent a call is for, or "".
func AgentOf(ctx Ctx) string {
	a, _ := ctx.Value(agentKey{}).(string)
	return a
}

// Confirmation is what an act tool will do, as the user is asked to say yes to it: a title, a lead sentence, facts
// as rows, a block of text or items (the prompt an agent will be given), and a note on what follows.
type Confirmation struct {
	Title string        `json:"title"`
	Lead  string        `json:"lead,omitempty"`
	Rows  []ConfirmRow  `json:"rows,omitempty"`
	Block *ConfirmBlock `json:"block,omitempty"`
	Note  string        `json:"note,omitempty"`
	// Line is the request in a few words, for a log: "start a review of acme/api#4410".
	Line string `json:"line"`
}

// ConfirmRow is one fact: "repository", "acme/api", and a link when it has one.
type ConfirmRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	URL   string `json:"url,omitempty"`
}

// ConfirmBlock is a confirmation's longer part: Text, or Items as a list.
type ConfirmBlock struct {
	Label string   `json:"label"`
	Text  string   `json:"text,omitempty"`
	Items []string `json:"items,omitempty"`
}

// Acted is what an agent tool that did something returns: what the agent reads, what was done in a few words, what
// opens it, and how to take it back.
type Acted struct {
	Result any
	Line   string // "started a review of acme/api#4410"
	Detail string // under it: "fake · m1 · review"
	// Open is a command that shows what it made: session.open with its id.
	Open     string
	OpenArgs Args
	// Undo takes it back, while the owner offers it; nil when it can't be.
	Undo func(ctx Ctx) error
}

// AgentOwner speaks for the user to a utility agent (the assistant): it asks them to confirm an act tool's call
// before it runs, and hears what the agent's tools did.
type AgentOwner struct {
	Agent   string
	Confirm func(ctx Ctx, tool string, c Confirmation) (bool, error)
	Acted   func(ctx Ctx, tool string, a Acted)
	Source  string
}

func (AgentOwner) ContributionKind() string   { return "agent.owner" }
func (o AgentOwner) ContributionName() string { return o.Agent }

// OwnerOf is the owner of an agent, if it has one.
func OwnerOf(r Registry, agent string) (AgentOwner, bool) {
	for _, c := range r.Contributions("agent.owner") {
		if o := c.(AgentOwner); o.Agent == agent {
			return o, true
		}
	}
	return AgentOwner{}, false
}

// ErrNotConfirmed is an act the user didn't say yes to: nothing was done.
var ErrNotConfirmed = errors.New("the user did not confirm, so nothing was done")

// CallAgentTool runs a tool for the agent named in ctx (WithAgent): an act asks the agent's owner first and runs
// only on a yes, and what the tool did goes to the owner. The answer is what the agent reads: a string as it is,
// anything else as JSON.
func CallAgentTool(ctx Ctx, r Registry, t AgentTool, params map[string]any) (string, error) {
	if t.Run == nil {
		return "", fmt.Errorf("%s does nothing", t.Name)
	}
	owner, owned := OwnerOf(r, AgentOf(ctx))
	if t.Confirm != nil {
		if !owned || owner.Confirm == nil {
			return "", fmt.Errorf("%s needs the user's yes, and there's no one to ask it of here", t.Name)
		}
		var c Confirmation
		err := Call(func() (err error) { c, err = t.Confirm(ctx, params); return })
		if err != nil {
			return "", err
		}
		yes, err := owner.Confirm(ctx, t.Name, c)
		if err != nil {
			return "", err
		}
		if !yes {
			return "", ErrNotConfirmed
		}
	}
	var out any
	err := Call(func() (err error) { out, err = t.Run(ctx, params); return })
	if err != nil {
		return "", err
	}
	if a, ok := out.(Acted); ok {
		if owned && owner.Acted != nil {
			owner.Acted(ctx, t.Name, a)
		}
		out = a.Result
		if out == nil {
			out = cmp.Or(a.Line, "done")
		}
	}
	if s, ok := out.(string); ok {
		return s, nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
}
