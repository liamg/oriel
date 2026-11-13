package host

import (
	"context"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// SetApp is agentrun.Runner.SetApp.
func (w *Host) SetApp(a App) { w.run.SetApp(a) }

// SetRestartOnExit is agentrun.Runner.SetRestartOnExit.
func (w *Host) SetRestartOnExit(on bool) { w.run.SetRestartOnExit(on) }

// Comment is agentrun.Runner.Comment, with a panic recovered.
func (w *Host) Comment(ctx context.Context, r CommentReq) (_ core.ThreadID, err error) {
	defer w.recover("Comment", &err)
	return w.run.Comment(ctx, r)
}

// SetThread is agentrun.Runner.SetThread, with a panic recovered.
func (w *Host) SetThread(r ThreadReq) (err error) {
	defer w.recover("SetThread", &err)
	return w.run.SetThread(r)
}

// EditQueued changes or deletes a comment of the user's the agent hasn't been given yet.
func (w *Host) EditQueued(r QueuedReq) (err error) {
	defer w.recover("EditQueued", &err)
	err = w.run.EditQueued(r)
	w.restatus(r.Session)
	return err
}

// Unqueue is agentrun.Runner.Unqueue, with a panic recovered.
func (w *Host) Unqueue(r UnqueueReq) (err error) {
	defer w.recover("Unqueue", &err)
	return w.run.Unqueue(r)
}

// SetTask is agentrun.Runner.SetTask, with a panic recovered.
func (w *Host) SetTask(id core.SessionID, t core.Task) (err error) {
	defer w.recover("SetTask", &err)
	return w.run.SetTask(id, t)
}

// Answer is agentrun.Runner.Answer, with a panic recovered.
func (w *Host) Answer(r AnswerReq) (err error) {
	defer w.recover("Answer", &err)
	return w.run.Answer(r)
}

// AddRule is agentrun.Runner.AddRule, with a panic recovered.
func (w *Host) AddRule(kind core.AskKind, rule string) (err error) {
	defer w.recover("AddRule", &err)
	return w.run.AddRule(kind, rule)
}

// RemoveRule is agentrun.Runner.RemoveRule, with a panic recovered.
func (w *Host) RemoveRule(rule string) (err error) {
	defer w.recover("RemoveRule", &err)
	return w.run.RemoveRule(rule)
}

// StartLogin is agentrun.Runner.StartLogin, with a panic recovered.
func (w *Host) StartLogin(ctx context.Context, provider string) (_ string, err error) {
	defer w.recover("StartLogin", &err)
	return w.run.StartLogin(ctx, provider)
}

// Ask is agentrun.Runner.Ask, with a panic recovered.
func (w *Host) Ask(ctx context.Context, r AskReq) (_ Verdict, err error) {
	defer w.recover("Ask", &err)
	return w.run.Ask(ctx, r)
}

// Send is agentrun.Runner.Send, with a panic recovered.
func (w *Host) Send(r SendReq) (err error) {
	defer w.recover("Send", &err)
	return w.run.Send(r)
}

// Fork is agentrun.Runner.Fork, with a panic recovered.
func (w *Host) Fork(ctx context.Context, id core.SessionID, prompt string) (_ string, err error) {
	defer w.recover("Fork", &err)
	return w.run.Fork(ctx, id, prompt)
}

// StopProcess is agentrun.Runner.StopProcess, with a panic recovered.
func (w *Host) StopProcess(ctx context.Context, id core.SessionID, process string) (err error) {
	defer w.recover("StopProcess", &err)
	return w.run.StopProcess(ctx, id, process)
}

// Interrupt is agentrun.Runner.Interrupt, with a panic recovered.
func (w *Host) Interrupt(id core.SessionID) (err error) {
	defer w.recover("Interrupt", &err)
	return w.run.Interrupt(id)
}

// Terminal is agentrun.Runner.Terminal, with a panic recovered.
func (w *Host) Terminal(ctx context.Context, id core.SessionID) (_ <-chan []byte, err error) {
	defer w.recover("Terminal", &err)
	return w.run.Terminal(ctx, id)
}

// TermInput is agentrun.Runner.TermInput, with a panic recovered.
func (w *Host) TermInput(r TermReq) (err error) {
	defer w.recover("TermInput", &err)
	return w.run.TermInput(r)
}

// SetPrompts is agentrun.Runner.SetPrompts, with a panic recovered.
func (w *Host) SetPrompts(o prompts.Overrides) (err error) {
	defer w.recover("SetPrompts", &err)
	return w.run.SetPrompts(o)
}

// Prompts is agentrun.Runner.Prompts, with a panic recovered.
func (w *Host) Prompts() (_ prompts.Overrides, _ bool) {
	defer w.recover("Prompts", nil)
	return w.run.Prompts()
}

// StartUtility is utility.Utilities.StartUtility, with a panic recovered.
func (w *Host) StartUtility(ctx context.Context, spec UtilitySpec) (_ *Utility, err error) {
	defer w.recover("StartUtility", &err)
	return w.utility.StartUtility(ctx, spec)
}

// AIReview is aireview.Reviews.AIReview, with a panic recovered.
func (w *Host) AIReview(ctx context.Context, r AIReviewReq) (_ string, err error) {
	defer w.recover("AIReview", &err)
	return w.reviews.AIReview(ctx, r)
}

// SetFindings is aireview.Reviews.SetFindings, with a panic recovered.
func (w *Host) SetFindings(r core.FindingsRecorded) (err error) {
	defer w.recover("SetFindings", &err)
	return w.reviews.SetFindings(r)
}

// RecordRun keeps a test run in its session, and returns its ID.
func (w *Host) RecordRun(id core.SessionID, run core.TestRun) (_ string, err error) {
	defer w.recover("RecordRun", &err)
	if run.ID == "" {
		run.ID = hostcore.NewID(4)
	}
	return run.ID, w.emit(core.TestRunRecorded{Session: id, Run: run})
}

// SetReviewed marks a file's changes reviewed, or takes the marks off.
func (w *Host) SetReviewed(r core.HunksReviewed) (err error) {
	defer w.recover("SetReviewed", &err)
	return w.emit(r)
}

// RecordVerified keeps a verify quiz's result in the session.
func (w *Host) RecordVerified(v core.Verified) (err error) {
	defer w.recover("RecordVerified", &err)
	return w.emit(v)
}
