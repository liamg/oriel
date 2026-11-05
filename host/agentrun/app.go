package agentrun

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/watch"
)

// App is what the host calls back in the app: the tools plugins offer agents, the policy hooks (layer 2), and a
// session's freshness manager, which the marks of what its shell commands changed watch its projects through. A nil
// func is no app: no tools, no hook answers, and every project marked each time.
type App struct {
	Tools    func(hostcore.ToolsReq) []agent.Tool
	CallTool func(context.Context, ToolCall) (string, error)
	Decide   func(context.Context, Decide) Verdict
	Watches  func(core.SessionID) *watch.Manager
}

// SetApp installs the app's callbacks; App{} takes them away.
func (x *Runner) SetApp(a App) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.app = a
}

func (x *Runner) appFuncs() App {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.app
}

// AppTools are the tools the app offers an agent, each called back in the app.
func (x *Runner) AppTools(r hostcore.ToolsReq) (list []agent.Tool) {
	defer x.env.Recover("the app's tools", nil)
	a := x.appFuncs()
	if a.Tools == nil {
		return nil
	}
	list = a.Tools(r)
	for i, t := range list {
		list[i].Call = x.appTool(ToolCall{Session: r.Session, Agent: r.Agent, Tool: t.Name})
	}
	return list
}

func (x *Runner) appTool(call ToolCall) func(context.Context, json.RawMessage) (string, error) {
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
func (x *Runner) decide(ctx context.Context, d Decide) (v Verdict, ok bool) {
	defer x.env.Recover("the app's policy hooks", nil)
	a := x.appFuncs()
	if a.Decide == nil {
		return Verdict{}, false
	}
	v = a.Decide(ctx, d)
	return v, v.Decision == "allow" || v.Decision == "deny"
}

// appWatches is session id's freshness manager in the app, or nil.
func (x *Runner) appWatches(id core.SessionID) (m *watch.Manager) {
	defer x.env.Recover("the app's watches", nil)
	if f := x.appFuncs().Watches; f != nil {
		return f(id)
	}
	return nil
}
