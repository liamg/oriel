package agentrun

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Begin hands a new session's agent its first request: the prompt and what came with it, as a thread the user sees
// in Requests; with none, the agent starts on its own if the session has one.
func (x *Runner) Begin(ctx context.Context, id core.SessionID, spec core.SessionSpec, k core.KindSpec) error {
	p, body := strings.TrimSpace(spec.Prompt), firstRequest(spec)
	if k.From == core.FromPR && p == "" {
		// The review is the agent's request, so it's one the user sees in Requests.
		p = "Review " + cmp.Or(spec.Title, "the pull request")
		body = strings.TrimSpace(fmt.Sprintf("%s: %s\n\n%s", p, spec.PR, body))
	}
	if body != "" {
		_, err := x.Comment(ctx, CommentReq{Session: id, Title: core.Summary(cmp.Or(p, body)), Body: body})
		return err
	}
	if spec.Provider != "" {
		x.StartAside(id)
	}
	return nil
}

// firstRequest is what the agent is first asked: the prompt, then what came with it.
func firstRequest(spec core.SessionSpec) string {
	p, c := strings.TrimSpace(spec.Prompt), strings.TrimSpace(spec.Context)
	switch {
	case c == "":
		return p
	case p == "":
		return c
	}
	return p + "\n\n" + c
}

// Configure switches the session's agent to another model or effort, its conversation kept, and says whether it was
// running, to be started again. It refuses mid-turn, which a restart would cut off.
func (x *Runner) Configure(r ConfigureReq) (bool, error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return false, err
	}
	switch {
	case se.Agent.Provider == "":
		return false, errors.New("this session has no agent")
	case se.Agent.Working:
		return false, errors.New("switch when the agent is idle")
	}
	running := se.Agent.Live()
	if err := x.WorkAgent(r.Session, StartWorkReq{Model: r.Model, Effort: r.Effort, KeepConversation: true}, "switching model"); err != nil {
		return false, err
	}
	return running, nil
}

// WorkAgent readies the session's agent: on the model, effort, sandbox and network r gives, restarted fresh on the
// work pack unless its conversation is kept, and restarted by its own id when only those changed; why is the stop's.
func (x *Runner) WorkAgent(id core.SessionID, r StartWorkReq, why string) error {
	se, err := x.st.Session(id)
	if err != nil {
		return err
	}
	a := se.Agent
	if a.Provider == "" {
		return nil
	}
	if r.Model != "" && r.Model != a.Model {
		a.Model, a.Effort = r.Model, ""
	}
	if r.Effort != "" {
		a.Effort = r.Effort
	}
	if r.Sandbox != nil && *r.Sandbox != a.Sandboxed {
		was := core.SessionSpec{Sandbox: core.SandboxPolicy{Container: a.Sandboxed}}
		if a.Policy == hostcore.Gate(was) {
			a.Policy = hostcore.Gate(core.SessionSpec{Sandbox: core.SandboxPolicy{Container: *r.Sandbox}})
		}
		a.Sandboxed = *r.Sandbox
	}
	if r.Network != "" {
		a.Network = r.Network
	}
	if !r.KeepConversation {
		a.Session, a.Pack = "", "work"
	}
	if a.Model == se.Agent.Model && a.Effort == se.Agent.Effort && a.Sandboxed == se.Agent.Sandboxed && a.Network == se.Agent.Network &&
		a.Session == se.Agent.Session && a.Pack == se.Agent.Pack {
		return nil
	}
	x.Stop(id, why, "")
	return x.st.Mutate(id, func(se core.SessionState) error {
		a.Running, a.Working, a.TurnStarted, a.Queue = se.Agent.Running, se.Agent.Working, se.Agent.TurnStarted, se.Agent.Queue
		return x.st.Emit(core.AgentChanged{ID: id, Agent: a})
	})
}
