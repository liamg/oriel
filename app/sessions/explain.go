package sessions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/agentkit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/registry"
)

// explain answers "ask to explain" from a tool-less fork of the session in front: it knows why the code is as
// it is, and changes nothing.
func (x *Sessions) explain(ctx context.Context, r registry.ExplainRequest) (string, error) {
	id := x.focused()
	if id == "" {
		return "", errors.New("no session is in front to explain from")
	}
	w, err := x.host()
	if err != nil {
		return "", err
	}
	how := map[registry.ExplainDepth]string{registry.ExplainDetailed: "in detail: what it does, step by step, and the cases it handles, in markdown",
		registry.ExplainWhy: "and why it is the way it is, from its history below and what you know of this session, in markdown"}[r.Depth]
	switch {
	case r.Format != "":
		how = "in the form asked for at the end"
	case how == "":
		how = "briefly, in a short paragraph, in markdown"
	}
	var b strings.Builder
	b.WriteString(kit.RenderPrompt(x.ConfigDir, "explain.request", map[string]string{"kind": r.Kind, "how": how}))
	fmt.Fprintf(&b, "\n\n%s\n\n%s", r.Subject, r.Context)
	if len(r.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles: %s", strings.Join(r.Files, ", "))
	}
	if r.Format != "" {
		b.WriteString("\n\n" + r.Format)
	}
	return w.Fork(ctx, hostapi.ForkReq{Session: id, Prompt: b.String()})
}

// explainer is who answers explain: the session in front's agent, forked.
func (x *Sessions) explainer() string {
	se, ok := x.h.Session(x.focused())
	if !ok {
		return "no session in front"
	}
	return "fork of " + cmp.Or(se.Snapshot().Agent.Provider, "the agent") + " · knows this session"
}

// explainerByline is who wrote a long answer, such as a walkthrough: the agent in front, from what it knows.
func (x *Sessions) explainerByline() string {
	se, ok := x.h.Session(x.focused())
	if !ok {
		return "written by the agent"
	}
	return "written by " + cmp.Or(se.Snapshot().Agent.Provider, "the agent") + " from the session's own context"
}

// fork answers a side question from a tool-less fork of the session's agent.
func (x *Sessions) fork(ctx context.Context, r registry.ForkRequest) (string, error) {
	w, err := x.host()
	if err != nil {
		return "", err
	}
	return w.Fork(ctx, hostapi.ForkReq{Session: core.SessionID(r.Session), Prompt: r.Prompt})
}

// forker is who answers a session's side questions: "claude · fork, no tools"; "" for an agent whose provider
// says it can't fork without tools (agent.Features), which answers none.
func (x *Sessions) forker(id string) string {
	se, ok := x.h.Session(core.SessionID(id))
	if !ok {
		return ""
	}
	p := se.Snapshot().Agent.Provider
	if f, known := agentkit.ProviderFeatures(x.h, p); known && !f.Fork {
		return ""
	}
	return cmp.Or(p, "the agent") + " · fork, no tools"
}
