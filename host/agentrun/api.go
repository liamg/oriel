package agentrun

import (
	"context"
	"errors"
	"fmt"

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

// Interrupt stops the turn the agent is on.
func (x *Runner) Interrupt(id core.SessionID) (err error) {
	h, _ := x.sessionFor(id, false)
	if h == nil || !h.running() {
		return errors.New("the agent isn't running")
	}
	h.urgent()
	return nil
}
