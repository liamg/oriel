package agentui

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// A question is asked in a thread: it's drawn at the foot of that thread's card wherever threads are drawn (between
// the editor's and the diff's lines, in Requests), and the inbox takes you there rather than stacking it over the
// window.

// questionFoot draws the questions waiting in a thread under its comments: each as its card, with its options and a
// field for an answer of your own.
func (x *Console) questionFoot(c *ui.Ctx, session, thread string, _ uireg.ThreadWhere, _ float32) {
	ref, ok := x.h.Session(core.SessionID(session))
	if !ok {
		return
	}
	se := ref.Snapshot()
	for _, a := range se.Asks {
		if a.Kind == core.AskQuestion && a.Thread != "" && string(a.Thread) == thread {
			c.Box("question."+a.ID, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.askCard(c, "card", se, a, uireg.CardPlace{}, false) })
		}
	}
}

// asksIn says a question waits in thread tid: its card shows, resolved or not, until it's answered.
func asksIn(se core.SessionState, tid core.ThreadID) bool {
	for _, a := range se.Asks {
		if a.Kind == core.AskQuestion && a.Thread != "" && a.Thread == tid {
			return true
		}
	}
	return false
}

// openThread shows thread tid of session id in Requests, bringing the session to the front first.
func (x *Console) openThread(id core.SessionID, tid core.ThreadID) {
	open := registry.Args{"thread": string(tid)}
	if x.h.State().Focus == id {
		x.run(context.Background(), "request.open", open)
		return
	}
	x.run(context.Background(), "session.open", registry.Args{"id": string(id), "then": []any{map[string]any{"command": "request.open", "args": open}}})
}
