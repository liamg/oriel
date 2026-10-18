package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/liamg/oriel/core"
)

// Answer answers an ask.
func (w *Worker) Answer(r AnswerReq) (err error) {
	defer w.recover("Answer", &err)
	h, _ := w.hostFor(r.Session, false)
	if h == nil {
		return errors.New("nothing in that session is asking")
	}
	return h.answer(r)
}

// Ask is the app asking the user for a session's agent, as its own gate would (an agent tool that asks first).
func (w *Worker) Ask(ctx context.Context, r AskReq) (_ Verdict, err error) {
	defer w.recover("Ask", &err)
	h, _ := w.hostFor(r.Session, false)
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
func (w *Worker) Send(r SendReq) (err error) {
	defer w.recover("Send", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if se.Agent.Provider == "" {
		return errors.New("this session has no agent")
	}
	h, err := w.hostFor(r.Session, true)
	if err != nil {
		return err
	}
	return h.queue(r.Text, r.Urgent)
}

// Fork answers a side question from the session's agent, outside its conversation.
func (w *Worker) Fork(id core.SessionID, prompt string) (_ string, err error) {
	defer w.recover("Fork", &err)
	h, _ := w.hostFor(id, false)
	if h == nil || !h.running() {
		return "", errors.New("the session's agent isn't running")
	}
	return h.fork(prompt)
}

// Interrupt stops the turn the agent is on.
func (w *Worker) Interrupt(id core.SessionID) (err error) {
	defer w.recover("Interrupt", &err)
	h, _ := w.hostFor(id, false)
	if h == nil || !h.running() {
		return errors.New("the agent isn't running")
	}
	h.urgent()
	return nil
}
