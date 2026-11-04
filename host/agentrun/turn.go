package agentrun

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// kick hands the agent whatever is waiting for it, if it's free to take it.
func (h *session) kick() {
	h.mu.Lock()
	if h.sess == nil || h.working || h.starting {
		h.mu.Unlock()
		return
	}
	sess := h.sess
	var t handout
	ok := false
	_ = h.r.st.Mutate(h.id, func(se core.SessionState) error {
		if h.r.SignedOut(se.Agent.Provider) {
			return nil
		}
		if t, ok = h.next(se); ok {
			h.give(se, t)
		}
		return nil
	})
	h.mu.Unlock()
	if !ok {
		return
	}
	hostcore.Restatus(h.r.st, h.id)
	h.send(sess, agent.Input{Text: t.text}, t.threads)
}

// handout is what the agent is handed next: a queued message, or a batch of comments and notes.
type handout struct {
	text     string
	queued   bool            // text is the queue's first message
	threads  []core.ThreadID // the threads in the batch
	comments map[string]bool // the comments it gives the agent
	notes    int             // how many of h.notes it carries
}

// next is what's waiting for the agent in se: queued messages first, then a batch of comments. Callers hold h.mu.
func (h *session) next(se core.SessionState) (handout, bool) {
	if len(se.Agent.Queue) > 0 {
		return handout{text: se.Agent.Queue[0], queued: true}, true
	}
	var batch []core.Thread
	fresh := map[string]bool{}
	for _, t := range se.Threads {
		if !t.Open() || t.Btw {
			continue
		}
		cs := t.Undelivered()
		if len(cs) == 0 {
			continue
		}
		for _, c := range cs {
			fresh[c.ID] = true
		}
		batch = append(batch, t)
	}
	if len(batch) == 0 && len(h.notes) == 0 {
		return handout{}, false
	}
	// Urgent threads go first.
	slices.SortStableFunc(batch, func(a, b core.Thread) int {
		switch {
		case a.Urgent && !b.Urgent:
			return -1
		case b.Urgent && !a.Urgent:
			return 1
		}
		return 0
	})
	t := handout{text: renderBatch(se, batch, fresh, h.notes), comments: fresh, notes: len(h.notes)}
	for _, th := range batch {
		t.threads = append(t.threads, th.ID)
	}
	return t, true
}

// give marks t as the agent's, in se as it is now, and the agent as working on it. Callers hold h.mu and the edit lock.
func (h *session) give(se core.SessionState, t handout) {
	if t.queued || len(t.comments) > 0 {
		h.carried = 0
	}
	if t.queued {
		a := se.Agent
		a.Queue = slices.Clone(a.Queue)
		if i := slices.Index(a.Queue, t.text); i >= 0 {
			a.Queue = slices.Delete(a.Queue, i, i+1)
		}
		_ = h.r.st.Emit(core.AgentChanged{ID: h.id, Agent: a})
		h.working = true
		return
	}
	for _, tid := range t.threads {
		th, ok := se.Thread(tid)
		if !ok {
			continue
		}
		th.Comments = slices.Clone(th.Comments)
		for i := range th.Comments {
			if t.comments[th.Comments[i].ID] {
				th.Comments[i].Delivered = true
			}
		}
		th.Busy, th.Active = true, len(t.threads) == 1
		th.Updated = h.r.env.Now().UTC()
		_ = h.r.st.Emit(core.ThreadUpdated{Session: h.id, Thread: th})
	}
	h.notes = h.notes[min(t.notes, len(h.notes)):]
	h.delivered = t.threads
	h.working = true
}

// send gives the agent a turn; if that fails, what it carried waits for the next.
func (h *session) send(sess agent.Session, in agent.Input, threads []core.ThreadID) {
	if err := sess.Send(h.ctx, in); err != nil {
		h.mu.Lock()
		h.working = false
		h.mu.Unlock()
		for _, tid := range threads {
			_, _ = h.r.mutateThread(h.id, tid, func(t *core.Thread) error {
				t.Busy, t.Active = false, false
				for i := range t.Comments {
					t.Comments[i].Delivered = t.Comments[i].Delivered && t.Comments[i].Author == core.AuthorAgent
				}
				return nil
			})
		}
		h.setAgent(func(a *core.AgentState) { a.Error = "couldn't reach the agent: " + err.Error() })
	}
}

// queue holds a message for the agent until its turn ends, or sends it now when it's free.
func (h *session) queue(text string, urgent bool) error {
	var queued error
	err := h.r.st.Mutate(h.id, func(se core.SessionState) error {
		a := se.Agent
		if urgent {
			a.Queue = append([]string{text}, a.Queue...)
		} else {
			a.Queue = append(slices.Clone(a.Queue), text)
		}
		queued = h.r.st.Emit(core.AgentChanged{ID: h.id, Agent: a})
		return nil
	})
	if err != nil {
		return err
	}
	if urgent {
		h.urgent()
	}
	h.kick()
	return queued
}

// fork asks the agent a side question outside the conversation.
func (h *session) fork(prompt string) (string, error) {
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil {
		return "", errors.New("the agent isn't running")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()
	return sess.Fork(ctx, agent.ForkSpec{Prompt: prompt})
}

// loop turns the agent's events into the session's state until it exits.
func (h *session) loop(sess agent.Session) {
	for ev := range sess.Events() {
		h.handle(sess, ev)
	}
	h.mu.Lock()
	mine := h.sess == sess
	if mine {
		h.sess, h.working = nil, false
	}
	box := h.box
	if mine {
		h.box = nil
	}
	h.mu.Unlock()
	if mine {
		if box != nil {
			_ = box.Close()
		}
		h.setAgent(func(a *core.AgentState) { a.Stopped(core.StoppedByAgent, h.r.env.Now()) })
		h.exited()
	}
}
