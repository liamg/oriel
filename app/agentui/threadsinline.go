package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// inlineNew is a thread being opened between the editor's lines (or beside a presentation's block), not sent yet.
// One opened by place has no buffer until its file is next decorated.
type inlineNew struct {
	path      string
	line, end int    // 0-based; end is the last line of a range, or -1
	text      string // the first line, as it was
	buf       *text.Buffer
	anchor    text.AnchorID
	draft     string
	focus     bool
	toAuthor  bool // a comment for the pull request's author, not a question for the agent
	review    bool // the session is a review: it can be either
}

func (n *inlineNew) release() {
	if n.buf != nil {
		n.buf.Release(n.anchor)
	}
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

// newInline opens a thread on {file}'s {line} (1-based) through {end_line}, or on the cursor's line: a composer between
// the lines, sent with ⏎.
func (x *Requests) newInline(a registry.Args) error {
	st := x.state("")
	n := &inlineNew{path: a.String("file"), line: a.Int("line") - 1, end: a.Int("end_line") - 1, focus: true}
	if se, ok := x.front(); ok && kit.ReviewedPR(x.h, se) != "" {
		n.review, n.toAuthor = true, a.String("to") != "agent"
	}
	if n.path == "" {
		ed, ok := registry.EditorsOf(x.h)
		if !ok {
			return errors.New("no editor")
		}
		b, ok := ed.Current()
		if !ok || b.Path == "" {
			return errors.New("the editor has no file here")
		}
		snap := b.Text.Snapshot()
		n.path, n.line, n.end, n.buf = b.Path, b.Cursor.Line, -1, b.Text
		n.text = string(snap.Line(n.line))
		n.anchor = n.buf.Anchor(snap.LineStart(n.line), text.Left)
	}
	if n.line < 0 {
		return errors.New("thread.new needs a line")
	}
	if o := st.inline; o != nil && filepath.Clean(o.path) == filepath.Clean(n.path) && o.line == n.line && o.end == n.end {
		n.release()
		o.focus, o.toAuthor = true, n.toAuthor
		return nil
	} else if o != nil {
		o.release()
	}
	st.inline = n
	return nil
}

// lineOf is where a thread is in the buffer being decorated, tracked by an anchor from the first time it's seen.
func (x *Requests) lineOf(ctx uireg.DecorationCtx, t core.Thread) int {
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

// threadsKey is what the threads' decorations of ctx are made from: the state, whether the window hides them, the
// thread to bring into view, and the composer being opened.
func (x *Requests) threadsKey(ctx uireg.DecorationCtx) any {
	type made struct {
		seq    uint64
		hidden bool
		reveal core.ThreadID
		inline *inlineNew
		buf    *text.Buffer
		focus  bool
	}
	st := x.h.State()
	k := made{seq: st.Seq, hidden: x.hiddenIn(ctx.Window, ctx.Path), reveal: x.reveal}
	if rs := x.state(st.Focus); rs.inline != nil {
		k.inline, k.buf, k.focus = rs.inline, rs.inline.buf, rs.inline.focus
	}
	return k
}

// decorations put the session's threads anchored in the file under the line each is about, with a mark in the
// gutter, and the composer of one being opened. In a presentation, where they're cards in its margin, the closed ones
// stay folded to a line. Where the window hides them, each line with threads gets a speech bubble instead.
func (x *Requests) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	se, ok := x.front()
	if !ok {
		return nil
	}
	st := x.state(se.ID)
	hidden := x.hiddenIn(ctx.Window, ctx.Path)
	present := ctx.Presenter != ""
	var out []uireg.Decoration
	byLine := map[int][]core.Thread{}
	var lines []int
	for _, t := range se.Threads {
		if t.Anchor == nil || t.Btw || !t.Open() && (!present || hidden) || filepath.Clean(kit.AnchorPath(se, t.Anchor)) != filepath.Clean(ctx.Path) {
			continue
		}
		line := x.lineOf(ctx, t)
		x.lineNow[t.ID] = line
		if line < ctx.Top || line >= ctx.Bottom {
			continue
		}
		if hidden {
			if byLine[line] == nil {
				lines = append(lines, line)
			}
			byLine[line] = append(byLine[line], t)
			continue
		}
		id, tok := t.ID, groupTone(se, t)
		reveal := x.reveal == id
		if reveal {
			x.reveal = ""
		}
		if !present {
			_, sign := statusLabel(se, t)
			out = append(out, uireg.Decoration{Kind: uireg.Sign, Line: line, Text: "▪", Style: sign.Name(), Priority: 20, Command: "request.open",
				Args: registry.Args{"thread": string(id)}, Tooltip: t.Heading()})
		}
		d := uireg.Decoration{Kind: uireg.Block, Line: line, Priority: 20, Reveal: reveal, Node: &ui.Func{Key: "thread." + string(id), Fn: func(c *ui.Ctx) {
			x.inlineThread(c, id, !present)
		}}}
		if t.Open() {
			d.Style = tok.Name()
		}
		out = append(out, d)
	}
	if hidden {
		bubbles := map[int]core.ThreadID{}
		for _, line := range lines {
			ts := byLine[line]
			bubbles[line] = ts[0].ID
			out = append(out, x.bubble(se, ctx.Window, line, ts))
		}
		x.bubbles[ctx.Window] = bubbles
	}
	if n := st.inline; n != nil && filepath.Clean(n.path) == filepath.Clean(ctx.Path) {
		if n.buf == nil && ctx.Buffer != nil && n.line < ctx.Snap.Lines() {
			n.buf, n.text = ctx.Buffer, string(ctx.Snap.Line(n.line))
			n.anchor = n.buf.Anchor(ctx.Snap.LineStart(n.line), text.Left)
		}
		off, ok := 0, false
		if n.buf != nil && n.buf == ctx.Buffer {
			off, ok = n.buf.Resolve(n.anchor)
		}
		if ok {
			line := ctx.Snap.PosAt(min(off, ctx.Snap.Len())).Line
			if line >= ctx.Top && line < ctx.Bottom {
				out = append(out, uireg.Decoration{Kind: uireg.Block, Line: line, Priority: 25, Reveal: n.focus,
					Node: &ui.Func{Key: "thread.new", Fn: func(c *ui.Ctx) { x.inlineComposer(c, st, n, line) }}})
			}
		}
	}
	return out
}

// groupTone is a thread's colour beside its lines: its state's, the accent while it's simply open.
func groupTone(se core.SessionState, t core.Thread) theme.TokenID {
	if kit.GroupOf(t) == kit.GroupOpen {
		return theme.Accent
	}
	_, tok := statusLabel(se, t)
	return tok
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

// inlineThread is a thread's card: between the editor's lines (with open, for its request), or in a presentation's
// margin, where a closed one folds to a line.
func (x *Requests) inlineThread(c *ui.Ctx, id core.ThreadID, open bool) {
	se, ok := x.front()
	if !ok {
		return
	}
	t, ok := se.Thread(id)
	if !ok {
		return
	}
	if t.Open() {
		delete(x.unfolded, id)
	} else if !x.unfolded[id] {
		if foldedThread(c, x.h, se, t, x.lineNow[id]) {
			x.unfolded[id] = true
		}
		return
	}
	st := x.state(se.ID)
	d := x.draftFor(st, id)
	focus := x.focusReply == id
	if focus {
		x.focusReply = ""
	}
	inlineThreadCard(c, x.h, se, t, &d.text, func() {
		if err := x.send(context.Background(), st, id, false); err != nil {
			x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}, threadCardOpts{Open: open, Defer: se.Planning(), Focus: focus, Fold: func() { delete(x.unfolded, id) }})
}

// foldedThread is a closed thread as one line: where it is, its state, what it asked and the answer, and reopen. It
// says whether the line was clicked, to unfold the thread for reading without reopening it.
func foldedThread(c *ui.Ctx, h plugin.Host, se core.SessionState, t core.Thread, line int) (unfold bool) {
	th := c.Theme()
	_, tok := statusLabel(se, t)
	c.Row("folded", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Radius: 6, Stroke: th.C(tReqThreadBd), Border: 1,
		Role: ui.RoleGroup, Label: "Thread " + t.Heading()}, func(c *ui.Ctx) {
		unfold = widget.Clickable(c, "unfold", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 8, Cross: ui.Center, Focusable: true, Role: ui.RoleButton,
			Label: "Show the thread", Tooltip: "Show the thread; it stays " + string(t.Status), Help: "agent/requests#inline"}, func(c *ui.Ctx) {
			fg := th.C(theme.FgMuted)
			c.Paint("chev", ui.Px(10), ui.Px(10), func(f render.Frame, r render.Rect) { paintChevron(f, r, fg, false) })
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tok)}, nil)
			c.Text("l", fmt.Sprintf("L%d", line+1), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold}, Color: th.C(theme.FgMuted)})
			widget.Chip(c, "status", string(t.Status), th.C(tok), "agent/requests#inline")
			quote := "“" + kit.FirstLine(t.Heading()) + "”"
			if last, ok := t.Last(); ok && last.Author == core.AuthorAgent {
				quote += " → " + core.Summary(last.Body)
			}
			c.Box("q", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", quote, kit.UIFont(11.5, th.C(theme.FgFaint))) })
		}).Clicked
		reopenLink(c, h, t.ID)
	})
	return unfold
}

// paintChevron draws a disclosure chevron into r: pointing down when open, right when not.
func paintChevron(f render.Frame, r render.Rect, fg render.Color, open bool) {
	c := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
	var p render.Path
	if open {
		p.MoveTo(render.Point{X: c.X - 4, Y: c.Y - 2}).LineTo(render.Point{X: c.X, Y: c.Y + 2}).LineTo(render.Point{X: c.X + 4, Y: c.Y - 2})
	} else {
		p.MoveTo(render.Point{X: c.X - 2, Y: c.Y - 4}).LineTo(render.Point{X: c.X + 2, Y: c.Y}).LineTo(render.Point{X: c.X - 2, Y: c.Y + 4})
	}
	f.Path(&p, render.Style{Stroke: fg, Width: 1.5})
}

// reopenLink is a closed thread's reopen.
func reopenLink(c *ui.Ctx, h plugin.Host, id core.ThreadID) {
	if widget.Link(c, "reopen", "reopen", "agent/requests#inline") {
		if err := h.Run(context.Background(), "request.reopen", registry.Args{"thread": string(id)}); err != nil {
			h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}
}

// threadCardOpts is what a thread's card offers beside resolving it and replying.
type threadCardOpts struct {
	Open  bool   // a link to its request in the main area
	Defer bool   // deferring it, so it doesn't hold up Start work
	Focus bool   // its reply field takes focus
	Fold  func() // collapses a closed thread back to its line
}

// inlineThreadCard is a thread as a card: its place, status and actions, the comments, and a reply field that send sends.
// It's the thread between the editor's lines and in a plan's margin.
func inlineThreadCard(c *ui.Ctx, h plugin.Host, se core.SessionState, t core.Thread, reply *string, send func(), o threadCardOpts) {
	th := c.Theme()
	id := t.ID
	run := func(cmd string, a registry.Args) {
		if err := h.Run(context.Background(), cmd, a); err != nil {
			h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
		}
	}
	src, sourced := uireg.ThreadSourceFor(h, t.Origin)
	title := "Thread"
	if sourced && src.Title != "" {
		title = src.Title
	}
	inlineCard(c, title+" "+t.Heading(), func(c *ui.Ctx, width float32) {
		word, tok := statusLabel(se, t)
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			if !t.Open() && o.Fold != nil && widget.IconButton(c, "fold", "", widget.IconButtonOpts{Label: "Collapse the thread",
				Help: "agent/requests#inline", Size: 18, Icon: func(c *ui.Ctx, fg render.Color) {
					c.Paint("chev", ui.Px(10), ui.Px(10), func(f render.Frame, r render.Rect) { paintChevron(f, r, fg, true) })
				}}) {
				o.Fold()
			}
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tok)}, nil)
			c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if sourced && src.Head != nil {
				src.Head(c, string(se.ID), string(id))
			} else {
				if t.Anchor != nil {
					c.Text("l", fmt.Sprintf("· L%d ·", t.Anchor.Line), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				}
				widget.Chip(c, "status", word, th.C(tok), "agent/requests#inline")
			}
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
			if !t.Open() && o.Fold != nil {
				reopenLink(c, h, id)
			}
			c.Text("id", "t:"+string(id)[:min(4, len(id))], kit.MonoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tReqThreadBd)}, nil)
		c.Column("comments", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 12}, func(c *ui.Ctx) {
			for _, cm := range t.Comments {
				inner := width - 62
				row := ui.Style{W: ui.Flex(1), Gap: 10}
				if cm.Draft {
					row.Pad, row.Fill, row.Stroke, row.Border, row.Radius = ui.Pad(8), th.C(tReqDraftBg), th.C(tReqDraftBd), 1, 6
					inner -= 18
				}
				c.Row(cm.ID, row, func(c *ui.Ctx) {
					avatar(c, "av", cm)
					c.Column("in", ui.Style{W: ui.Px(inner), Gap: 4}, func(c *ui.Ctx) {
						c.Row("who", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
							name := cmp.Or(cm.Name, "you")
							if cm.Author == core.AuthorAgent {
								name = kit.ProviderName(cm.Name)
							}
							c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
							if when := commentWhen(c.Now(), cm, t.Origin); when != "" {
								c.Text("t", when, kit.MonoFont(9.5, th.C(theme.FgGhost)))
							}
							if cm.Redacted {
								widget.Chip(c, "redacted", "hidden from the agent", th.C(theme.FgFaint), "git/github#trusted")
							}
						})
						kit.MarkdownTextIn(c, h, "body", cm.Body, inner, kit.UIFont(12.5, th.C(theme.FgMuted)), kit.ThreadMarkdown(se, t))
						if sourced && src.Comment != nil {
							src.Comment(c, string(se.ID), string(id), cm.ID, inner)
						}
						if len(cm.Evidence) > 0 {
							c.Row("ev", ui.Style{Gap: 6}, func(c *ui.Ctx) {
								for i, e := range cm.Evidence {
									kit.EvidenceChip(c, i, h, se.ID, e.Ref, e.Label, e.OK)
								}
							})
						}
					})
				})
			}
			if t.Busy {
				workingLine(c, "working", se, ui.Flex(1))
			}
			uireg.DrawThreadFeet(h, c, string(se.ID), string(id), width-24)
			if !t.Open() {
				return
			}
			if o.Focus {
				c.RequestFocus(c.Next("reply"))
			}
			if widget.Prose(c, "reply", reply, widget.ProseOpts{Label: "Reply to the thread", Placeholder: "Reply…", Lines: commentLines, EnterSubmits: true,
				Help: "agent/requests#inline"}).Submitted {
				send()
			}
		})
	})
}

// commentWhen is the small text after a comment's author: how long ago, where it came from, and whether it was sent.
func commentWhen(now time.Time, cm core.Comment, origin string) string {
	var parts []string
	switch {
	case cm.Draft:
		return "draft · not sent"
	case !cm.Time.IsZero():
		parts = append(parts, kit.Ago(now, cm.Time))
	}
	switch {
	case cm.Author == core.AuthorHost:
		parts = append(parts, origin)
	case !cm.Sent.IsZero():
		parts = append(parts, "sent to "+origin)
	}
	if cm.Edited {
		parts = append(parts, "edited")
	}
	return strings.Join(parts, " · ")
}

// commentLines is how many lines a comment's field grows to before it scrolls.
const commentLines = 8

// inlineComposer is the card of a thread being opened on a line: ⏎ sends it, ⇧⏎ starts a new line, Escape drops it.
func (x *Requests) inlineComposer(c *ui.Ctx, st *requestsState, n *inlineNew, line int) {
	th := c.Theme()
	title, label, hint := "New thread", "Ask about this line", "Ask the agent about this line"
	if n.toAuthor {
		title, label, hint = "Comment for the author", "Comment for the author", "A comment for the author, in your review"
	}
	inlineCard(c, title, func(c *ui.Ctx, width float32) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.Accent)}, nil)
			c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("l", fmt.Sprintf("· L%d", line+1), kit.MonoFont(10.5, th.C(theme.FgFaint)))
			if n.review {
				to := 1
				if n.toAuthor {
					to = 0
				}
				if widget.Segmented(c, "to", []string{"To the author", "Ask the agent"}, &to, widget.SegmentedOpts{Label: "Who it's for", Help: "agent/reviews#comments"}) {
					n.toAuthor = to == 0
				}
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "⏎ sends · ⇧⏎ new line · esc drops it", kit.MonoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 10}}, func(c *ui.Ctx) {
			id := c.Next("input")
			if n.focus {
				c.RequestFocus(id)
				n.focus = false
			}
			r := widget.Prose(c, "input", &n.draft, widget.ProseOpts{Label: label, Placeholder: hint,
				Lines: commentLines, EnterSubmits: true, Help: "agent/requests#inline"})
			switch {
			case r.Submitted && strings.TrimSpace(n.draft) != "":
				a := registry.Args{"body": strings.TrimSpace(n.draft), "file": n.path, "line": line + 1, "text": strings.TrimRight(n.text, "\r\n")}
				if n.end > n.line {
					a["end_line"] = line + 1 + n.end - n.line
				}
				if n.toAuthor {
					a["outgoing"] = true
				}
				if err := x.h.Run(context.Background(), "request.comment", a); err != nil {
					x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
					return
				}
				n.release()
				st.inline = nil
			case r.Cancelled:
				n.release()
				st.inline = nil
			}
		})
	})
}
