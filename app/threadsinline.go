package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// inlineNew is a thread being opened between the editor's lines, not sent yet.
type inlineNew struct {
	path   string
	text   string // the line, as it was
	buf    *text.Buffer
	anchor text.AnchorID
	draft  string
	focus  bool
}

// threadAnchor is a thread's place in one buffer, which moves with its edits.
type threadAnchor struct {
	buf    *text.Buffer
	thread core.ThreadID
}

// anchoredLine is where an anchor's line is in lines now (0-based): its line, or the nearest that still reads as
// it did when the thread was made.
func anchoredLine(lines []string, a *core.Anchor) int {
	want := max(a.Line-1, 0)
	first := ""
	for _, l := range strings.Split(a.Text, "\n") {
		if first = strings.TrimSpace(l); first != "" {
			break
		}
	}
	same := func(i int) bool { return i >= 0 && i < len(lines) && strings.TrimSpace(lines[i]) == first }
	if first == "" || same(want) || len(lines) == 0 {
		return min(want, max(len(lines)-1, 0))
	}
	for d := 1; d <= 200; d++ {
		if same(want - d) {
			return want - d
		}
		if same(want + d) {
			return want + d
		}
	}
	return min(want, len(lines)-1)
}

// newInline opens a thread on the cursor's line: a composer between the lines, sent with ⏎.
func (x *requests) newInline() error {
	if x.sh.editor == nil || x.sh.editor.e == nil {
		return errors.New("no editor")
	}
	p := x.sh.editor.current()
	path := p.Name()
	if path == "" {
		return errors.New("the editor has no file here")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(x.sh.editor.dir, path)
	}
	snap, line := p.Buf(), p.Cursor().Line
	st := x.state("")
	if n := st.inline; n != nil {
		n.buf.Release(n.anchor)
	}
	st.inline = &inlineNew{path: path, text: string(snap.Line(line)), buf: p.Buffer(), anchor: p.Buffer().Anchor(snap.LineStart(line), text.Left), focus: true}
	return nil
}

// lineOf is where a thread is in the buffer being decorated, tracked by an anchor from the first time it's seen.
func (x *requests) lineOf(ctx registry.DecorationCtx, t core.Thread) int {
	if ctx.Buffer == nil {
		return anchoredLine(nil, t.Anchor)
	}
	k := threadAnchor{ctx.Buffer, t.ID}
	if id, ok := x.anchors[k]; ok {
		if off, ok := ctx.Buffer.Resolve(id); ok {
			return ctx.Snap.PosAt(min(off, ctx.Snap.Len())).Line
		}
	}
	lines := strings.Split(ctx.Snap.String(), "\n")
	line := anchoredLine(lines, t.Anchor)
	x.anchors[k] = ctx.Buffer.Anchor(ctx.Snap.LineStart(min(line, ctx.Snap.Lines()-1)), text.Left)
	return line
}

// decorations put the session's open threads between the editor's lines, under the line each is about, with a
// mark in the gutter; and the composer of one being opened.
func (x *requests) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	se, ok := x.front()
	if !ok {
		return nil
	}
	st := x.state(se.ID)
	var out []registry.Decoration
	for _, t := range se.Threads {
		if t.Anchor == nil || !t.Open() || t.Btw || filepath.Clean(anchorPath(se, t.Anchor)) != filepath.Clean(ctx.Path) {
			continue
		}
		line := x.lineOf(ctx, t)
		if line < ctx.Top || line >= ctx.Bottom {
			continue
		}
		id := t.ID
		_, tok := statusLabel(se, t)
		out = append(out,
			registry.Decoration{Kind: registry.Sign, Line: line, Text: "▪", Style: tok.Name(), Priority: 20, Command: "request.open",
				Args: registry.Args{"thread": string(id)}, Tooltip: t.Heading()},
			registry.Decoration{Kind: registry.Block, Line: line, Priority: 20, Node: &ui.Func{Key: "thread." + string(id), Fn: func(c *ui.Ctx) {
				x.inlineThread(c, id)
			}}})
	}
	if n := st.inline; n != nil && n.buf == ctx.Buffer {
		if off, ok := n.buf.Resolve(n.anchor); ok {
			line := ctx.Snap.PosAt(min(off, ctx.Snap.Len())).Line
			if line >= ctx.Top && line < ctx.Bottom {
				out = append(out, registry.Decoration{Kind: registry.Block, Line: line, Priority: 25, Reveal: n.focus,
					Node: &ui.Func{Key: "thread.new", Fn: func(c *ui.Ctx) { x.inlineComposer(c, st, n, line) }}})
			}
		}
	}
	return out
}

// inlineCard is a thread's card between the lines: its header, a body, at most 780 wide.
func inlineCard(c *ui.Ctx, label string, body func(c *ui.Ctx, width float32)) {
	th := c.Theme()
	width := float32(780)
	if r, ok := c.Rect(c.Next("wrap")); ok {
		width = min(r.Dx(), 780)
	}
	c.Row("wrap", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Column("card", ui.Style{W: ui.Px(width), Fill: th.C(tReqThreadBg), Stroke: th.C(tReqThreadBd), Border: 1, Radius: 6, Clip: true,
			Role: ui.RoleGroup, Label: label, Help: "agent/requests#inline"}, func(c *ui.Ctx) { body(c, width) })
	})
}

func (x *requests) inlineThread(c *ui.Ctx, id core.ThreadID) {
	se, ok := x.front()
	if !ok {
		return
	}
	t, ok := se.Thread(id)
	if !ok {
		return
	}
	st := x.state(se.ID)
	d := x.draftFor(st, id)
	inlineThreadCard(c, x.h, se, t, &d.text, func() {
		if err := x.send(context.Background(), st, id, false); err != nil {
			x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}, threadCardOpts{Open: true})
}

// threadCardOpts is what a thread's card offers beside resolving it and replying.
type threadCardOpts struct {
	Open  bool // a link to its request in the main area
	Defer bool // deferring it, so it doesn't hold up Start work
}

// inlineThreadCard is a thread as a card: its place, status and actions, the comments, and a reply field that send sends.
// It's the thread between the editor's lines and in a plan's margin.
func inlineThreadCard(c *ui.Ctx, h ext.Host, se core.SessionState, t core.Thread, reply *string, send func(), o threadCardOpts) {
	th := c.Theme()
	id := t.ID
	run := func(cmd string, a registry.Args) {
		if err := h.Run(context.Background(), cmd, a); err != nil {
			h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}
	inlineCard(c, "Thread "+t.Heading(), func(c *ui.Ctx, width float32) {
		word, tok := statusLabel(se, t)
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tok)}, nil)
			c.Text("t", "Thread", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if t.Anchor != nil {
				c.Text("l", fmt.Sprintf("· L%d ·", t.Anchor.Line), monoFont(10.5, th.C(theme.FgFaint)))
			}
			widget.Chip(c, "status", word, th.C(tok), "agent/requests#inline")
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if o.Open && widget.Link(c, "open", "open ↗", "agent/requests#inline") {
				run("request.open", registry.Args{"thread": string(id)})
			}
			if t.Open() && o.Defer && widget.Link(c, "defer", "defer", "agent/plan#readiness") {
				run("request.resolve", registry.Args{"thread": string(id), "status": string(core.ThreadDeferred)})
			}
			if t.Open() && widget.Link(c, "resolve", "resolve", "agent/requests#inline") {
				run("request.resolve", registry.Args{"thread": string(id)})
			}
			c.Text("id", "t:"+string(id)[:min(4, len(id))], monoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tReqThreadBd)}, nil)
		c.Column("comments", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 12}, func(c *ui.Ctx) {
			for _, cm := range t.Comments {
				c.Row(cm.ID, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					avatar(c, "av", cm)
					c.Column("in", ui.Style{W: ui.Px(width - 62), Gap: 4}, func(c *ui.Ctx) {
						c.Row("who", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
							name := cmpOr(cm.Name, "you")
							if cm.Author == core.AuthorAgent {
								name = providerName(cm.Name)
							}
							c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
							if !cm.Time.IsZero() {
								c.Text("t", ago(c.Now(), cm.Time), monoFont(9.5, th.C(theme.FgGhost)))
							}
						})
						markdownText(c, "body", cm.Body, width-62, uiFont(12.5, th.C(theme.FgMuted)))
						if len(cm.Evidence) > 0 {
							c.Row("ev", ui.Style{Gap: 6}, func(c *ui.Ctx) {
								for i, e := range cm.Evidence {
									evidenceChip(c, i, h, se.ID, e.Ref, e.Label, e.OK)
								}
							})
						}
					})
				})
			}
			if widget.Input(c, "reply", reply, widget.InputOpts{Label: "Reply to the thread", Placeholder: "Reply…", Help: "agent/requests#inline"}).Submitted {
				send()
			}
		})
	})
}

// inlineComposer is the card of a thread being opened on a line: ⏎ sends it, Escape drops it.
func (x *requests) inlineComposer(c *ui.Ctx, st *requestsState, n *inlineNew, line int) {
	th := c.Theme()
	inlineCard(c, "New thread", func(c *ui.Ctx, width float32) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.Accent)}, nil)
			c.Text("t", "New thread", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("l", fmt.Sprintf("· L%d", line+1), monoFont(10.5, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "⏎ sends · esc drops it", monoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 10}}, func(c *ui.Ctx) {
			id := c.Next("input")
			if n.focus && c.Interaction(id).Focused {
				n.focus = false
			} else if n.focus {
				c.Focus(id)
			}
			r := widget.Input(c, "input", &n.draft, widget.InputOpts{Label: "Ask about this line", Placeholder: "Ask the agent about this line",
				Help: "agent/requests#inline"})
			switch {
			case r.Submitted && strings.TrimSpace(n.draft) != "":
				a := registry.Args{"body": strings.TrimSpace(n.draft), "file": n.path, "line": line + 1, "text": strings.TrimRight(n.text, "\r\n")}
				if err := x.h.Run(context.Background(), "request.comment", a); err != nil {
					x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
					return
				}
				n.buf.Release(n.anchor)
				st.inline = nil
			case r.Cancelled:
				n.buf.Release(n.anchor)
				st.inline = nil
			}
		})
	})
}
