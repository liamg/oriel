package api

import (
	"cmp"
	"context"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=thread cap=agent.prompt
type Thread struct{ a *APIs }

//oriel:api doc="The requests (threads) of the session in front, each { id, title, status (open, resolved, deferred, dismissed), file, line, busy (the agent has it), urgent, btw, comments (how many), last (the last comment's text), author (who said it: you, agent) }." returns=table cap=agent.read
func (t Thread) List() []any {
	st := t.a.Host.State()
	se, ok := st.Session(st.Focus)
	if !ok {
		return []any{}
	}
	ths := slices.Clone(se.Snapshot().Threads)
	out := make([]any, 0, len(ths))
	for _, th := range ths {
		row := script.Table{"id": string(th.ID), "title": th.Heading(), "status": cmp.Or(string(th.Status), string(core.ThreadOpen)),
			"busy": th.Busy, "urgent": th.Urgent, "btw": th.Btw, "comments": len(th.Comments)}
		if a := th.Anchor; a != nil {
			row["file"], row["line"] = a.File, a.Line
		}
		if c, ok := th.Last(); ok {
			row["last"], row["author"] = c.Body, string(c.Author)
		}
		out = append(out, row)
	}
	return out
}

//oriel:api doc="Open a request: with opts.body, send it now ({ body, file, line, urgent, btw }; no file is about the whole change); without, open a thread on the editor's line at the cursor to write it." optional=opts
func (t Thread) New(opts script.Table) error {
	if str(opts, "body") == "" {
		return t.a.Host.Run(context.Background(), "thread.new", nil)
	}
	return t.a.Host.Run(context.Background(), "request.comment", commentArgs(opts))
}

//oriel:api doc="Reply to a request by id: opts { urgent (interrupts the agent's turn), btw (a side question, answered by a fork) }." optional=opts
func (t Thread) Reply(id, body string, opts script.Table) error {
	a := commentArgs(opts)
	a["thread"], a["body"] = id, body
	return t.a.Host.Run(context.Background(), "request.comment", a)
}

//oriel:api doc="Resolve a request by id, or set its status: open, resolved, deferred or dismissed." optional=status
func (t Thread) Resolve(id, status string) error {
	return t.a.Host.Run(context.Background(), "request.resolve", registry.Args{"thread": id, "status": status})
}

//oriel:api doc="Show a request by id in the Requests view."
func (t Thread) Open(id string) error {
	return t.a.Host.Run(context.Background(), "request.open", registry.Args{"thread": id})
}

func commentArgs(o script.Table) registry.Args {
	a := registry.Args{"body": str(o, "body"), "file": expand(str(o, "file"))}
	if l, ok := o["line"].(float64); ok {
		a["line"] = int(l)
	}
	for _, k := range []string{"urgent", "btw"} {
		if b, ok := o[k].(bool); ok {
			a[k] = b
		}
	}
	return a
}
