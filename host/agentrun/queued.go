package agentrun

import (
	"errors"
	"slices"
	"strings"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

type QueuedReq = hostapi.QueuedReq

// EditQueued changes or deletes a queued comment. It refuses one the agent already has: it can't be taken back.
func (x *Runner) EditQueued(r QueuedReq) error {
	if !r.Delete && strings.TrimSpace(r.Body) == "" {
		return errors.New("the comment is empty: delete it instead")
	}
	_, err := x.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
		i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return c.ID == r.Comment })
		switch {
		case i < 0:
			return errors.New("no such comment")
		case t.Comments[i].Author != core.AuthorYou:
			return errors.New("only your own comments can be changed")
		case t.Comments[i].Delivered:
			return errors.New("the agent already has it")
		}
		t.Comments = slices.Clone(t.Comments)
		if r.Btw && !r.Delete {
			c := t.Comments[i]
			c.Body, c.Edited, c.Btw, c.Time = strings.TrimSpace(r.Body), true, true, x.env.Now().UTC()
			t.Comments = append(slices.Delete(t.Comments, i, i+1), c)
			return nil
		}
		if r.Delete {
			t.Comments = slices.Delete(t.Comments, i, i+1)
			if len(t.Comments) == 0 {
				t.Status, t.Busy = core.ThreadDismissed, false // a request with nothing left in it
			}
			return nil
		}
		t.Comments[i].Body = strings.TrimSpace(r.Body)
		t.Comments[i].Edited = true
		return nil
	})
	switch {
	case err != nil || r.Delete:
		return err
	case r.Btw:
		hostcore.Restatus(x.st, r.Session)
		x.env.Go("a btw", func() { x.answerBtw(r.Session, r.Thread) })
		return nil
	case r.Urgent:
		t, err := x.mutateThread(r.Session, r.Thread, func(t *core.Thread) error { t.Urgent = true; return nil })
		if err != nil {
			return err
		}
		if h, _ := x.sessionFor(r.Session, false); h != nil && t.Open() && len(t.Undelivered()) > 0 {
			h.urgent()
			h.kick()
		}
	}
	return nil
}

type UnqueueReq = hostapi.UnqueueReq

// Unqueue drops a queued message. One the agent has been given already is gone from the queue, so it refuses.
func (x *Runner) Unqueue(r UnqueueReq) (err error) {
	defer x.env.Recover("Unqueue", &err)
	var refused error
	merr := x.st.Mutate(r.Session, func(se core.SessionState) error {
		a := se.Agent
		if r.Index < 0 || r.Index >= len(a.Queue) || a.Queue[r.Index] != r.Text {
			refused = errors.New("the agent already has it")
			return nil
		}
		a.Queue = slices.Delete(slices.Clone(a.Queue), r.Index, r.Index+1)
		refused = x.st.Emit(core.AgentChanged{ID: r.Session, Agent: a})
		return nil
	})
	return errors.Join(merr, refused)
}
