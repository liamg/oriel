package agentrun

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// Answer answers an ask.
func (x *Runner) Answer(r AnswerReq) (err error) {
	h, _ := x.sessionFor(r.Session, false)
	if h == nil {
		return errors.New("nothing in that session is asking")
	}
	return h.answer(r)
}

// Ask is the app asking the user for a session's agent, as its own gate would (an agent tool that asks first).
func (x *Runner) Ask(ctx context.Context, r AskReq) (_ Verdict, err error) {
	h, _ := x.sessionFor(r.Session, false)
	if h == nil {
		return Verdict{}, fmt.Errorf("session %s isn't running", r.Session)
	}
	if r.Ask.Kind == "" {
		r.Ask.Kind = core.AskPermission
	}
	a, err := h.approve(ctx, r.Ask)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Decision: "deny", By: a.By, Note: a.Note}
	if a.Allow {
		v.Decision = "allow"
	}
	return v, nil
}

// Send is a message to the agent outside any thread: sent now if it's free, queued for the turn's end if not.
func (x *Runner) Send(r SendReq) (err error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	if se.Agent.Provider == "" {
		return errors.New("this session has no agent")
	}
	h, err := x.sessionFor(r.Session, true)
	if err != nil {
		return err
	}
	return h.queue(r.Text, r.Urgent)
}

// Fork answers a side question from the session's agent, outside its conversation.
func (x *Runner) Fork(id core.SessionID, prompt string) (_ string, err error) {
	h, _ := x.sessionFor(id, false)
	if h == nil || !h.running() {
		return "", errors.New("the session's agent isn't running")
	}
	return h.fork(prompt)
}

// StopProcess stops a process the agent's last turn left running (AgentState.Processes), by its ID; an error if its
// provider can't.
func (x *Runner) StopProcess(ctx context.Context, id core.SessionID, process string) error {
	h, _ := x.sessionFor(id, false)
	if h == nil || !h.running() {
		return errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	ps, ok := sess.(agent.ProcessStopper)
	if !ok {
		se, _ := x.st.Session(id)
		return fmt.Errorf("%s can't stop the processes it leaves running", se.Agent.Provider)
	}
	if err := ps.StopProcess(ctx, process); err != nil {
		return err
	}
	h.setAgent(func(a *core.AgentState) {
		a.Processes = slices.DeleteFunc(slices.Clone(a.Processes), func(t core.BackgroundTask) bool { return t.ID == process })
	})
	return nil
}

// Interrupt stops the turn the agent is on.
func (x *Runner) Interrupt(id core.SessionID) (err error) {
	h, _ := x.sessionFor(id, false)
	if h == nil || !h.running() {
		return errors.New("the agent isn't running")
	}
	h.urgent()
	return nil
}
