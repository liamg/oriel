package agentui

import (
	"errors"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// doIt is what accepting a proposal says in its thread, as if you'd typed it.
const doIt = "Do it"

// proposed says thread t ends on an agent's reply offering a change it hasn't made.
func proposed(t core.Thread) bool {
	c, ok := t.Last()
	return ok && c.Author == core.AuthorAgent && c.Proposes && !c.Progress
}

// proposalThread is the thread {thread} of session {id} (the one in front by default) while it ends on a proposal.
func (x *Requests) proposalThread(a registry.Args) (core.SessionID, core.Thread, bool) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = x.h.State().Focus
	}
	ref, ok := x.h.Session(id)
	if !ok {
		return id, core.Thread{}, false
	}
	t, ok := ref.Snapshot().Thread(core.ThreadID(a.String("thread")))
	return id, t, ok && proposed(t)
}

func (x *Requests) proposalCommand() registry.Command {
	return registry.Command{ID: "thread.proposal.accept", Grant: string(plugin.CapAgentPrompt),
		Title:     "Have the agent make the change it proposed in {thread} of {id} (the session in front by default): your \"Do it\" in the thread",
		Args:      []registry.Arg{registry.StringArg("id"), registry.StringArg("thread")},
		Available: func(a registry.Args) bool { _, _, ok := x.proposalThread(a); return ok },
		Run: func(ctx registry.Ctx, a registry.Args) error {
			id, t, ok := x.proposalThread(a)
			if !ok {
				return errors.New("the thread doesn't end on a proposal")
			}
			return x.h.Run(ctx, "request.comment", registry.Args{"id": string(id), "thread": string(t.ID), "body": doIt})
		}}
}

// proposalFoot offers Do it under a thread that ends on a proposal.
func (x *Requests) proposalFoot(c *ui.Ctx, session, thread string, where uireg.ThreadWhere, width float32) {
	if where == uireg.ThreadInFinding {
		return
	}
	a := registry.Args{"id": session, "thread": thread}
	if _, _, ok := x.proposalThread(a); !ok {
		return
	}
	row := ui.Style{W: ui.Px(width), Gap: 6}
	if where == uireg.ThreadInRequest {
		row.Pad.L = commentIndent // under the comments' text, past their avatars
	}
	c.Row("proposal", row, func(c *ui.Ctx) {
		if widget.Button(c, "do", doIt, widget.ButtonOpts{Primary: true, Help: "Have the agent make the change it proposed"}) {
			x.run("thread.proposal.accept", a)
		}
	})
}
