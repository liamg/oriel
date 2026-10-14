package app

import (
	"strings"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

var (
	tAgentEdited     = theme.DeclareOn("agent.edited.bg", "alpha(green, 7%)", "bg.base")
	tAgentEditedEdge = theme.Declare("agent.edited.edge", "green")
)

// agentEditShown is how long the agent's latest edit to a file shows.
const agentEditShown = 10 * time.Minute

// agentEdits decorate the lines the agent's latest edit to the file wrote, where they still read as it left them:
// each lit, with what it replaced (a one-line change), and "agent edited 4s ago" after the first.
func (x *requests) agentEdits(ctx registry.DecorationCtx) []registry.Decoration {
	se, ok := x.front()
	if !ok || ctx.Snap.Len() == 0 {
		return nil
	}
	for i := len(se.Transcript) - 1; i >= 0; i-- {
		it := se.Transcript[i]
		if it.Diff == nil || !sameFile(se, it.Diff.Path, ctx.Path) {
			continue
		}
		if ctx.Now.Sub(it.Time) > agentEditShown {
			return nil
		}
		lines := strings.Split(strings.TrimSuffix(ctx.Snap.String(), "\n"), "\n")
		var out []registry.Decoration
		first, removed := -1, ""
		for _, l := range it.Diff.Lines {
			switch l.Op {
			case "-":
				if removed == "" {
					removed = l.Text
				}
				continue
			case "+":
				if l.New > 0 && l.New <= len(lines) && lines[l.New-1] == l.Text {
					out = append(out, registry.Decoration{Kind: registry.LineBg, Line: l.New - 1, Style: tAgentEdited.Name(), Edge: tAgentEditedEdge.Name(),
						Was: removed, Priority: 5})
					if first < 0 || l.New-1 < first {
						first = l.New - 1
					}
				}
			}
			removed = ""
		}
		if first >= 0 {
			out = append(out, registry.Decoration{Kind: registry.VirtualText, Line: first, Text: "agent edited " + editedAgo(ctx.Now, it.Time),
				Style: tAgentEditedEdge.Name(), Priority: 5})
		}
		return out
	}
	return nil
}
