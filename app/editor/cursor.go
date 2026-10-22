package editor

import (
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// lineCursor is one of the multi-cursors on a line: its head's column (-1 if it isn't on the line), and the part of
// its region the line holds (-1 for none).
type lineCursor struct{ head, selFrom, selTo int }

// otherCursors are the cursors of a multi-cursor edit on line, in the current window.
func (x *Editor) otherCursors(p vim.Pane, line int) []lineCursor {
	if !p.Current() {
		return nil
	}
	m, ok := x.e.Multi()
	if !ok {
		return nil
	}
	var out []lineCursor
	n := len(p.Buf().Line(line))
	for _, r := range m.Regions {
		lc := lineCursor{head: -1, selFrom: -1, selTo: -1}
		if r.Head.Line == line {
			lc.head = r.Head.Col
		}
		if m.Extend {
			a, b := r.Anchor, r.Head
			if b.Line < a.Line || b.Line == a.Line && b.Col < a.Col {
				a, b = b, a
			}
			if line >= a.Line && line <= b.Line {
				lc.selFrom, lc.selTo = 0, n
				if line == a.Line {
					lc.selFrom = a.Col
				}
				if line == b.Line {
					lc.selTo = min(b.Col+1, n)
				}
			}
		}
		if lc.head >= 0 || lc.selFrom >= 0 {
			out = append(out, lc)
		}
	}
	return out
}

// cursorTrail is where the cursor was last frame, and when it last moved.
type cursorTrail struct {
	seen      bool
	line, col int
	top       int
	at        time.Time
}

type cursorTrailKey struct{}

// cursorKey is an eased part of the cursor: "x", "y", "w", "h".
type cursorKey string

// cursorFast is how soon after the last move a move snaps rather than glides: held keys and quick runs.
const cursorFast = 90 * time.Millisecond

// cursorGlide is how the cursor is drawn against where it is: off by dx, dy, and its size eased as its shape
// changes (unless it's new).
type cursorGlide struct {
	on, moving bool
	dx, dy     float32
	first      bool
	c          *ui.Ctx
}

func (g cursorGlide) w(v float32) float32 { return g.size("w", v) }

func (g cursorGlide) h(v float32) float32 { return g.size("h", v) }

func (g cursorGlide) size(k cursorKey, v float32) float32 {
	if g.first {
		return g.c.Snap(k, v)
	}
	return g.c.Ease(k, v)
}

// glideCursor glides the cursor between positions on the motion spring (editor.cursor_glide). A step of one column
// on the same line, a move within cursorFast of the last, and a scroll snap, so it never trails typing or held keys.
// c is the window's lines, which the cursor's place is measured in.
func (x *Editor) glideCursor(c *ui.Ctx, p vim.Pane, rowID ui.ID, line, vc int, cx, cy float32, shape vim.Shape, m kit.PaneMetrics) cursorGlide {
	if on, _, err := x.h.Options().Get("editor.cursor_glide", registry.At{}); err == nil && on == false {
		return cursorGlide{}
	}
	lines, ok1 := c.Rect(c.ID())
	row, ok2 := c.Rect(rowID)
	tr := ui.State[cursorTrail](c, ui.Child(c.ID(), cursorTrailKey{}))
	first := !tr.seen
	now := c.Now()
	moved := tr.line != line || tr.col != vc
	snap := !tr.seen || !ok1 || !ok2 || p.Top() != tr.top ||
		moved && (tr.line == line && max(vc-tr.col, tr.col-vc) <= 1 || now.Sub(tr.at) < cursorFast)
	if moved {
		tr.at = now
	}
	tr.seen, tr.line, tr.col, tr.top = true, line, vc, p.Top()
	ty := cy
	if shape == vim.Underline {
		ty += m.LineH - 4
	}
	tx, ty := row.Min.X-lines.Min.X+cx, row.Min.Y-lines.Min.Y+ty
	ease := c.Ease
	if snap {
		ease = c.Snap
	}
	ex, ey := ease(cursorKey("x"), tx), ease(cursorKey("y"), ty)
	g := cursorGlide{on: true, dx: ex - tx, dy: ey - ty, first: first, c: c}
	g.moving = max(g.dx, -g.dx) > m.CharW*.35 || max(g.dy, -g.dy) > 2
	return g
}
