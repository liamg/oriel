package sessions

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// agentTools are the tools plugins offer an agent, as the host hands them on.
func (x *Sessions) agentTools(h plugin.Host, r hostapi.ToolsReq) []agent.Tool {
	var out []agent.Tool
	name, utility := x.agentName(r.Session, r.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		out = append(out, agent.Tool{Name: t.Name, Description: t.Desc, Schema: t.Schema(), ReadOnly: t.ReadOnly && t.Confirm == nil,
			CoveredBy: t.CoveredBy})
	}
	return out
}

// gatewayTools are the tools plugins add to a sandboxed agent's gateway, as the host runs them.
func gatewayTools(h plugin.Host) []hostapi.GatewayTool {
	var out []hostapi.GatewayTool
	for _, t := range registry.ContributionsOf[registry.GatewayTool](h) {
		out = append(out, hostapi.GatewayTool{Name: t.Name, Desc: t.Desc, InputSchema: t.InputSchema, ReadOnly: t.ReadOnly,
			Run: func(ctx context.Context, c hostapi.GatewayCall) (out string, err error) {
				err = registry.RecoverAsError(func() (err error) {
					out, err = t.Run(ctx, registry.GatewayCall{Session: string(c.Session), Project: c.Project, Dir: c.Dir, Env: c.Env, Args: c.Args})
					return err
				})

				return out, err
			}})
	}
	return out
}

// agentName is the agent the host asks for: a session's, by its kind, or a utility agent with no session.
func (x *Sessions) agentName(id core.SessionID, agent string) (string, bool) {
	if id == "" {
		return agent, true
	}
	if agent == "" {
		if se, ok := x.h.Session(id); ok {
			agent = string(se.Kind())
		}
	}
	return agent, false
}

// callTool runs a tool for an agent. A session's agent is owned by the host: an act there is asked through the
// session's rules, the policy hooks and then the user's card, as the agent's own gate is.
func (x *Sessions) callTool(ctx context.Context, h plugin.Host, w hostapi.SessionCalls, call hostapi.ToolCall) (string, error) {
	name, utility := x.agentName(call.Session, call.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		if t.Name != call.Tool || t.Run == nil {
			continue
		}
		var params map[string]any
		if len(call.Args) > 0 {
			if err := json.Unmarshal(call.Args, &params); err != nil {
				return "", err
			}
		}
		ctx := registry.WithAgent(registry.WithSession(ctx, string(call.Session)), name)
		if call.Session != "" {
			ctx = registry.WithOwner(ctx, registry.AgentOwner{Agent: name, Source: "builtin:sessions",
				Confirm: func(ctx registry.Ctx, tool string, cf registry.Confirmation) (bool, error) {
					return askForSession(ctx, w, call, cf)
				}})
		}
		return registry.CallAgentTool(ctx, h, t, params)
	}
	return "", fmt.Errorf("no tool %q", call.Tool)
}

// askForSession asks for a session's agent's act. The rule "Always" makes is the tool's name.
func askForSession(ctx context.Context, w hostapi.SessionCalls, call hostapi.ToolCall, cf registry.Confirmation) (bool, error) {
	tool := "mcp__oriel__" + call.Tool
	v, err := w.Ask(ctx, hostapi.AskReq{Session: call.Session, Ask: core.Ask{Kind: core.AskPermission, Tool: tool,
		Summary: cmp.Or(cf.Line, cf.Title), Detail: cf.Lead, Input: string(call.Args), Rule: tool}})
	if err != nil {
		return false, err
	}
	if v.Decision == hostapi.DecisionAllow {
		return true, nil
	}
	if v.Note != "" {
		return false, fmt.Errorf("denied by %s: %s", cmp.Or(v.By, "the user"), v.Note)
	}
	return false, fmt.Errorf("denied by %s, so nothing was done", cmp.Or(v.By, "the user"))
}

// decide runs the Permission hooks on an ask: layer 2, before it reaches the user.
func (x *Sessions) decide(ctx context.Context, h plugin.Host, d hostapi.Decide) hostapi.Verdict {
	ev := registry.PermissionEvent{Session: string(d.Session), Kind: string(d.Ask.Kind), Tool: d.Ask.Tool, Summary: d.Ask.Summary,
		Input: kit.MainInput(d.Ask.Input), Rule: d.Ask.Rule, Host: d.Ask.Host}
	subject := d.Ask.Tool
	if subject == "" {
		subject = d.Ask.Host
	}
	var v registry.Verdict
	done := make(chan struct{})
	h.Do(func() {
		defer close(done)
		v, _ = h.Fire(ctx, "Permission", subject, ev)
	})
	select {
	case <-done:
	case <-ctx.Done():
		return hostapi.Verdict{}
	}
	switch {
	case v.Veto:
		return hostapi.Verdict{Decision: hostapi.DecisionDeny, Note: v.Reason, By: "a Permission hook"}
	case !v.Decided:
		return hostapi.Verdict{}
	}
	return hookVerdict(v.Value)
}

// hookVerdict reads a Permission hook's answer, in any of the shapes it may give ("allow", true, a
// registry.PermissionVerdict, a {decision, note} table), as allow or deny; anything else leaves the ask to the user.
func hookVerdict(answer any) hostapi.Verdict {
	var dec, note string
	switch val := answer.(type) {
	case string:
		dec = val
	case bool:
		dec = map[bool]string{true: "allow", false: "deny"}[val]
	case registry.PermissionVerdict:
		dec, note = val.Decision, val.Note
	case map[string]any:
		dec, _ = val["decision"].(string)
		note, _ = val["note"].(string)
	}
	if d := hostapi.Decision(dec); d.Decided() {
		return hostapi.Verdict{Decision: d, Note: note, By: "a Permission hook"}
	}
	return hostapi.Verdict{}
}
