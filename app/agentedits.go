package app

import (
	"slices"
	"sort"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
)

var (
	tAgentEdited     = theme.DeclareOn("agent.edited.bg", "alpha(green, 7%)", "bg.base")
	tAgentEditedEdge = theme.Declare("agent.edited.edge", "green")
)

// agentEditShown is how long the agent's latest edit to a file shows.
const agentEditShown = 10 * time.Minute

// transcriptKey is a session's transcript as it is now, and where its relative paths are: the state replaces a
// transcript when it changes, never changing one in place.
type transcriptKey struct {
	items *core.Item
	n     int
	dir   string
}

func transcriptOf(se core.SessionState) transcriptKey {
	k := transcriptKey{n: len(se.Transcript)}
	if k.n > 0 {
		k.items = &se.Transcript[0]
	}
	if p, ok := se.Primary(); ok {
		k.dir = p.Dir
	}
	return k
}

// agentEdit is the agent's latest edit to a file, found in a transcript, and its lines a buffer's version still reads
// as it left them.
type agentEdit struct {
	of      transcriptKey
	item    *core.Item // nil for none
	buf     *text.Buffer
	version uint64
	lit     []registry.Decoration // by line; nil until they're found
	first   int
}

// agentEdits decorate the lines the agent's latest edit to the file wrote, where they still read as it left them:
// each lit, with what it replaced (a one-line change), and "agent edited 4s ago" after the first.
func (x *requests) agentEdits(ctx registry.DecorationCtx) []registry.Decoration {
	se, ok := x.front()
	if !ok || ctx.Snap.Len() == 0 {
		return nil
	}
	if x.edits == nil {
		x.edits = map[string]*agentEdit{}
	}
	e := x.edits[ctx.Path]
	if of := transcriptOf(se); e == nil || e.of != of {
		e = &agentEdit{of: of, item: latestEdit(se, ctx.Path)}
		x.edits[ctx.Path] = e
	}
	it := e.item
	if it == nil || ctx.Now.Sub(it.Time) > agentEditShown {
		return nil
	}
	if e.lit == nil || e.buf != ctx.Buffer || e.version != ctx.Snap.Version() {
		e.buf, e.version = ctx.Buffer, ctx.Snap.Version()
		e.lit, e.first = editedLines(it.Diff, ctx.Snap)
	}
	lo := sort.Search(len(e.lit), func(i int) bool { return e.lit[i].Line >= ctx.Top })
	hi := sort.Search(len(e.lit), func(i int) bool { return e.lit[i].Line >= ctx.Bottom })
	out := slices.Clone(e.lit[lo:max(lo, hi)])
	if e.first >= 0 {
		out = append(out, registry.Decoration{Kind: registry.VirtualText, Line: e.first, Text: "agent edited " + editedAgo(ctx.Now, it.Time),
			Style: tAgentEditedEdge.Name(), Priority: 5})
	}
	next := time.Minute
	if ctx.Now.Sub(it.Time) < time.Minute {
		next = time.Second
	}
	ctx.RefreshAt(ctx.Now.Add(next))
	ctx.RefreshAt(it.Time.Add(agentEditShown))
	return out
}

// latestEdit is the transcript's latest edit to path.
func latestEdit(se core.SessionState, path string) *core.Item {
	for i := len(se.Transcript) - 1; i >= 0; i-- {
		if it := &se.Transcript[i]; it.Diff != nil && sameFile(se, it.Diff.Path, path) {
			return it
		}
	}
	return nil
}

// editedLines are the lines of snap the diff wrote that still read as it left them, lit, and the first of them (-1
// for none).
func editedLines(d *core.FileDiff, snap text.Snapshot) ([]registry.Decoration, int) {
	n := snap.Lines()
	if snap.LineStart(n-1) == snap.Len() {
		n-- // the empty line after the last newline
	}
	out := []registry.Decoration{}
	first, removed := -1, ""
	for _, l := range d.Lines {
		switch l.Op {
		case "-":
			if removed == "" {
				removed = l.Text
			}
			continue
		case "+":
			if l.New > 0 && l.New <= n && string(snap.Line(l.New-1)) == l.Text {
				out = append(out, registry.Decoration{Kind: registry.LineBg, Line: l.New - 1, Style: tAgentEdited.Name(), Edge: tAgentEditedEdge.Name(),
					Was: removed, Priority: 5})
				if first < 0 || l.New-1 < first {
					first = l.New - 1
				}
			}
		}
		removed = ""
	}
	slices.SortStableFunc(out, func(a, b registry.Decoration) int { return a.Line - b.Line })
	return out, first
}
