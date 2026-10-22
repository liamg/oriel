package host

import (
	"context"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// SetApp installs the app's callbacks; App{} takes them away.
func (w *Host) SetApp(a App) { w.run.SetApp(a) }

// Comment adds your comment to a thread, opening one if needed, and hands it to the agent: at once if it's idle,
// after an interrupt if urgent, else when the turn ends.
func (w *Host) Comment(ctx context.Context, r CommentReq) (_ core.ThreadID, err error) {
	defer w.recover("Comment", &err)
	return w.run.Comment(ctx, r)
}

// SetThread changes a thread's status. Resolving one completes the tasks it asked for.
func (w *Host) SetThread(r ThreadReq) (err error) {
	defer w.recover("SetThread", &err)
	return w.run.SetThread(r)
}

// SetTask adds or changes a task from the app.
func (w *Host) SetTask(id core.SessionID, t core.Task) (err error) {
	defer w.recover("SetTask", &err)
	return w.run.SetTask(id, t)
}

// Answer answers an ask.
func (w *Host) Answer(r AnswerReq) (err error) {
	defer w.recover("Answer", &err)
	return w.run.Answer(r)
}

// Ask is the app asking the user for a session's agent, as its own gate would (an agent tool that asks first).
func (w *Host) Ask(ctx context.Context, r AskReq) (_ Verdict, err error) {
	defer w.recover("Ask", &err)
	return w.run.Ask(ctx, r)
}

// Send is a message to the agent outside any thread: sent now if it's free, queued for the turn's end if not.
func (w *Host) Send(r SendReq) (err error) {
	defer w.recover("Send", &err)
	return w.run.Send(r)
}

// Fork answers a side question from the session's agent, outside its conversation.
func (w *Host) Fork(id core.SessionID, prompt string) (_ string, err error) {
	defer w.recover("Fork", &err)
	return w.run.Fork(id, prompt)
}

// Interrupt stops the turn the agent is on.
func (w *Host) Interrupt(id core.SessionID) (err error) {
	defer w.recover("Interrupt", &err)
	return w.run.Interrupt(id)
}

// Terminal is the agent's raw terminal: what it has shown, then what it shows, until ctx ends or the agent
// stops.
func (w *Host) Terminal(ctx context.Context, id core.SessionID) (_ <-chan []byte, err error) {
	defer w.recover("Terminal", &err)
	return w.run.Terminal(ctx, id)
}

// TermInput types into the agent's terminal, and resizes it.
func (w *Host) TermInput(r TermReq) (err error) {
	defer w.recover("TermInput", &err)
	return w.run.TermInput(r)
}

// SetPrompts is the user's versions of the prompts (agent/prompts), sent by the app on connect and whenever one
// changes. They're the source of truth from then on: a built-in pack is theirs or the default, never an older
// file's. Each workspace's notes are written again with the coding style.
func (w *Host) SetPrompts(o prompts.Overrides) (err error) {
	defer w.recover("SetPrompts", &err)
	return w.run.SetPrompts(o)
}

// Prompts is the user's versions of the prompts the app last sent, and whether it has.
func (w *Host) Prompts() (_ prompts.Overrides, _ bool) {
	defer w.recover("Prompts", nil)
	return w.run.Prompts()
}

// StartUtility starts a utility agent; it runs until Close.
func (w *Host) StartUtility(ctx context.Context, spec UtilitySpec) (_ *Utility, err error) {
	defer w.recover("StartUtility", &err)
	return w.utility.StartUtility(ctx, spec)
}

// AIReview reviews a session's change with an agent other than the session's own. It runs
// in the background; its findings are session events as they come.
func (w *Host) AIReview(ctx context.Context, r AIReviewReq) (_ string, err error) {
	defer w.recover("AIReview", &err)
	return w.reviews.AIReview(ctx, r)
}

// SetFindings records findings as the user changed them: dismissed, or back.
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

// SetSeen records lines of a file's change seen in the diff.
func (w *Host) SetSeen(r core.LinesSeen) (err error) {
	defer w.recover("SetSeen", &err)
	return w.emit(r)
}

// RecordOverride records a commit made past the commit gate.
func (w *Host) RecordOverride(r core.CommitGateOverridden) (err error) {
	defer w.recover("RecordOverride", &err)
	return w.emit(r)
}

// RecordVerified keeps a verify quiz's result in the session.
func (w *Host) RecordVerified(v core.Verified) (err error) {
	defer w.recover("RecordVerified", &err)
	return w.emit(v)
}
