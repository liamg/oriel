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
func AgentTools(r Contributions) []AgentTool {
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

// PermissionEvent is what the "Permission" hooks see before an ask reaches the user. A
// Decide hook answers with "allow" or "deny", or a PermissionVerdict to give the agent a reason.
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

// AgentToolsFor are the agent tools offered to an agent (OfferedTo), by name; not the parts other tools' answers add.
func AgentToolsFor(r Contributions, agent string, utility bool) []AgentTool {
	return slices.DeleteFunc(AgentTools(r), func(t AgentTool) bool { return t.Adds != "" || !t.OfferedTo(agent, utility) })
}

// AgentToolParts are the parts added to tool's answer (AgentTool.Adds) for an agent, by name.
func AgentToolParts(r Contributions, tool, agent string, utility bool) []AgentTool {
	return slices.DeleteFunc(AgentTools(r), func(t AgentTool) bool { return t.Adds != tool || !t.OfferedTo(agent, utility) })
}

// partKey is where a tool's result goes in an answer with parts.
func (t AgentTool) partKey() string { return cmp.Or(t.Reads, t.Name) }

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

type ownerKey struct{}

// WithOwner is ctx for a call whose agent's owner is o, ahead of any registered: a session's agent is owned by its
// worker, which asks through the session's rules, the policy hooks and then the user.
func WithOwner(ctx Ctx, o AgentOwner) Ctx { return context.WithValue(ctx, ownerKey{}, o) }

// OwnerOf is the owner of an agent, if it has one.
func OwnerOf(r Contributions, agent string) (AgentOwner, bool) {
	for _, c := range r.Contributions("agent.owner") {
		if o := c.(AgentOwner); o.Agent == agent {
			return o, true
		}
	}
	return AgentOwner{}, false
}

// ErrNotConfirmed is an act the user didn't say yes to: nothing was done.
var ErrNotConfirmed = errors.New("the user did not confirm, so nothing was done")

// Granted is what an AgentTool's Confirm returns when this call needs no yes: the user has already granted it.
var Granted = errors.New("granted")

func ownerOf(ctx Ctx, r Registry) (AgentOwner, bool) {
	if o, ok := ctx.Value(ownerKey{}).(AgentOwner); ok {
		return o, true
	}
	return OwnerOf(r, AgentOf(ctx))
}

// CallAgentTool runs a tool for the agent named in ctx (WithAgent): an act asks the agent's owner (WithOwner, else
// the registered one) first and runs only on a yes, unless its Confirm says Granted; what the tool did goes to the
// owner. The answer is what the agent reads: a string as it is, anything else as JSON.
func CallAgentTool(ctx Ctx, r Registry, t AgentTool, params map[string]any) (string, error) {
	if t.Run == nil {
		return "", fmt.Errorf("%s does nothing", t.Name)
	}
	owner, owned := ownerOf(ctx, r)
	if t.Confirm != nil {
		var c Confirmation
		err := Call(func() (err error) { c, err = t.Confirm(ctx, params); return })
		if err != nil && !errors.Is(err, Granted) {
			return "", err
		}
		if err == nil {
			if err := confirm(ctx, owner, owned, t.Name, c); err != nil {
				return "", err
			}
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
	if parts := partsOf(ctx, r, t); len(parts) > 0 {
		all := map[string]any{t.partKey(): out}
		for _, p := range parts {
			var v any
			if err := Call(func() (err error) { v, err = p.Run(ctx, params); return }); err != nil {
				v = map[string]string{"error": err.Error()}
			}
			all[p.partKey()] = v
		}
		out = all
	}
	if s, ok := out.(string); ok {
		return s, nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
}

// partsOf are the parts added to t's answer for the agent in ctx: any agent's, as a session's tools are offered.
func partsOf(ctx Ctx, r Registry, t AgentTool) []AgentTool {
	var out []AgentTool
	for _, p := range AgentTools(r) {
		if p.Adds == t.Name && p.Run != nil && (len(p.For) == 0 || slices.Contains(p.For, AgentOf(ctx))) {
			out = append(out, p)
		}
	}
	return out
}

// confirm asks owner for the user's yes to c.
func confirm(ctx Ctx, owner AgentOwner, owned bool, tool string, c Confirmation) error {
	if !owned || owner.Confirm == nil {
		return fmt.Errorf("%s needs the user's yes, and there's no one to ask it of here", tool)
	}
	yes, err := owner.Confirm(ctx, tool, c)
	if err != nil {
		return err
	}
	if !yes {
		return ErrNotConfirmed
	}
	return nil
}
