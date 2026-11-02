package agentrun

import (
	"errors"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
)

// QueuedReq changes a comment of the user's the agent hasn't been given yet: Body in its place, or with Delete, the
// comment taken out. The comments after it stay as they are, after it.
type QueuedReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	Body    string         `json:"body,omitempty"`
	Delete  bool           `json:"delete,omitempty"`
}

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
	return err
}

// UnqueueReq takes a message for the agent out of the session's queue before it's sent: the one at Index, which must
// still be Text.
type UnqueueReq struct {
	Session core.SessionID `json:"session"`
	Index   int            `json:"index"`
	Text    string         `json:"text"`
}

// Unqueue drops a queued message. One the agent has been given already is gone from the queue, so it refuses.
func (x *Runner) Unqueue(r UnqueueReq) error {
	var err error
	merr := x.st.Mutate(r.Session, func(se core.SessionState) error {
		a := se.Agent
		if r.Index < 0 || r.Index >= len(a.Queue) || a.Queue[r.Index] != r.Text {
			err = errors.New("the agent already has it")
			return nil
		}
		a.Queue = slices.Delete(slices.Clone(a.Queue), r.Index, r.Index+1)
		err = x.st.Emit(core.AgentChanged{ID: r.Session, Agent: a})
		return nil
	})
	return errors.Join(merr, err)
}
