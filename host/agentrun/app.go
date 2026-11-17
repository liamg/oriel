package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/agent"
)

// SetApp installs the app's callbacks; App{} takes them away.
func (x *Runner) SetApp(a hostapi.App) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.app = a
}

func (x *Runner) appFuncs() hostapi.App {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.app
}

// GatewayTools are the tools the app's plugins add to a sandboxed agent's gateway.
func (x *Runner) GatewayTools() []hostapi.GatewayTool {
	defer x.env.Recover("the app's gateway tools", nil)
	if a := x.appFuncs(); a.GatewayTools != nil {
		return a.GatewayTools()
	}
	return nil
}

// AppTools are the tools the app offers an agent, each called back in the app.
func (x *Runner) AppTools(r hostapi.ToolsReq) (list []agent.Tool) {
	defer x.env.Recover("the app's tools", nil)
	a := x.appFuncs()
	if a.Tools == nil {
		return nil
	}
	list = x.uncovered(r, a.Tools(r))
	for i, t := range list {
		list[i].Call = x.appTool(hostapi.ToolCall{Session: r.Session, Agent: r.Agent, Tool: t.Name})
	}
	return list
}

func (x *Runner) appTool(call hostapi.ToolCall) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (out string, err error) {
		defer x.env.Recover("the app's tool "+call.Tool, &err)
		a := x.appFuncs()
		if a.CallTool == nil {
			return "", errors.New("Oriel's window isn't open, and this tool runs there")
		}
		call.Args = args
		return a.CallTool(ctx, call)
	}
}

// decide asks the app's policy hooks about an ask; ok is false when none answers.
func (x *Runner) decide(ctx context.Context, d hostapi.Decide) (v hostapi.Verdict, ok bool) {
	defer x.env.Recover("the app's policy hooks", nil)
	a := x.appFuncs()
	if a.Decide == nil {
		return hostapi.Verdict{}, false
	}
	v = a.Decide(ctx, d)
	return v, v.Decision.Decided()
}

// uncovered are tools less those an MCP server the session's agent reaches already covers (agent.Tool's CoveredBy).
func (x *Runner) uncovered(r hostapi.ToolsReq, tools []agent.Tool) []agent.Tool {
	if r.Session == "" || x.deps.MCPServers == nil || !slices.ContainsFunc(tools, func(t agent.Tool) bool { return len(t.CoveredBy) > 0 }) {
		return tools
	}
	se, err := x.st.Session(r.Session)
	if err != nil {
		return tools
	}
	servers := x.deps.MCPServers(se)
	return slices.DeleteFunc(tools, func(t agent.Tool) bool { return agent.Covered(servers, t.CoveredBy) })
}
