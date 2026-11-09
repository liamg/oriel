package agentui

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// threadsPlusKey is what the lines' + is made from: the session in front and the file.
func (x *Requests) threadsPlusKey(ctx uireg.DecorationCtx) any {
	type made struct {
		focus core.SessionID
		path  string
	}
	return made{x.h.State().Focus, ctx.Path}
}

// threadsPlus is a + in the gutter of every line, shown while the pointer is on the line, that opens a thread on it
// (thread.new): to ask the agent about it, or ask for a change.
func (x *Requests) threadsPlus(ctx uireg.DecorationCtx) []uireg.Decoration {
	if ctx.Path == "" || x.h.State().Focus == "" {
		return nil
	}
	return []uireg.Decoration{{Kind: uireg.Sign, Line: uireg.EveryLine, Text: "+", Style: "accent", OnHover: true, Command: "thread.new",
		Args: registry.Args{"file": ctx.Path}, Tooltip: "Comment on line {line}"}}
}
