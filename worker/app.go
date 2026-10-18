package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/liamg/oriel/agent"
)

// App is what the host calls back in the app: the tools plugins offer agents, and the policy hooks (layer 2).
// A nil func is no app: no tools, and no hook answers.
type App struct {
	Tools    func(ToolsReq) []agent.Tool
	CallTool func(context.Context, ToolCall) (string, error)
	Decide   func(context.Context, Decide) Verdict
}

// SetApp installs the app's callbacks; App{} takes them away.
func (w *Worker) SetApp(a App) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.app = a
}

func (w *Worker) appFuncs() App {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.app
}

// appTools are the tools the app offers an agent, each called back in the app.
func (w *Worker) appTools(r ToolsReq) (list []agent.Tool) {
	defer w.recover("the app's tools", nil)
	a := w.appFuncs()
	if a.Tools == nil {
		return nil
	}
	list = a.Tools(r)
	for i, t := range list {
		list[i].Call = w.appTool(ToolCall{Session: r.Session, Agent: r.Agent, Tool: t.Name})
	}
	return list
}

func (w *Worker) appTool(call ToolCall) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (out string, err error) {
		defer w.recover("the app's tool "+call.Tool, &err)
		a := w.appFuncs()
		if a.CallTool == nil {
			return "", errors.New("Oriel's window isn't open, and this tool runs there")
		}
		call.Args = args
		return a.CallTool(ctx, call)
	}
}

// decide asks the app's policy hooks about an ask; ok is false when none answers.
func (w *Worker) decide(ctx context.Context, d Decide) (v Verdict, ok bool) {
	defer w.recover("the app's policy hooks", nil)
	a := w.appFuncs()
	if a.Decide == nil {
		return Verdict{}, false
	}
	v = a.Decide(ctx, d)
	return v, v.Decision == "allow" || v.Decision == "deny"
}
