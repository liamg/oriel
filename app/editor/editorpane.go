package editor

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

var (
	tCrumbFg       = theme.Declare("editor.breadcrumb.fg", "fg.faint")
	tCrumbLast     = theme.Declare("editor.breadcrumb.current", "fg.muted")
	tGutterCur     = theme.Declare("editor.gutter.current", "fg.faint")
	tCursorBg      = theme.Declare("editor.cursor", "accent")
	tCursorFg      = theme.Declare("editor.cursor.fg", "accent.ink")
	tFoldBg        = theme.DeclareOn("editor.fold.bg", "alpha(fg, 5%)", "bg.base")
	tFoldFg        = theme.Declare("editor.fold.fg", "fg.faint")
	tCmdlineBg     = theme.Declare("editor.cmdline.bg", "bg.raised")
	tModeFg        = theme.Declare("statusline.mode.fg", "accent.ink")
	tModeNormal    = theme.Declare("statusline.mode.normal", "accent")
	tModeInsert    = theme.Declare("statusline.mode.insert", "status.quiet")
	tModeVisual    = theme.Declare("statusline.mode.visual", "status.reviewer")
	tModeCommand   = theme.Declare("statusline.mode.command", "status.working")
	tModeRecording = theme.Declare("statusline.mode.recording", "error")
)

// metrics are the panes' for editor.font_size and editor.line_height now.
func (x *Editor) metrics(c *ui.Ctx) kit.PaneMetrics {
	x.pane = kit.Metrics(c, x.h.Options(), x.pane)
	return x.pane
}

// paneView draws window id: its breadcrumb, then its lines from the window's top, and the cursor in the
// current window. Keys typed at it go to the engine after the keymap registry passes on them.
func (x *Editor) paneView(c *ui.Ctx, id vim.WindowID, focused bool) {
	if x.conv.due() {
		x.conv.refresh()
	}
	m := x.metrics(c)
	p, ok := x.e.Pane(id)
	if !ok {
		c.Text("closed", "this window is closed", ui.TextStyle{})
		return
	}
	th := c.Theme()
	x.checkDisk(c)
	x.now = c.Now()
	wp, pr, presented := x.presenting(p)
	codeID := ui.Child(c.Next("pane"), "code")
	in := c.Interaction(codeID)
	if focused && !p.Current() {
		x.e.EnterWindow(id)
	}
	st := x.inputFor(p)
	if p.Current() {
		for _, k := range in.Keys {
			r := st.Key(kit.Notation(k))
			if r.Command != "" {
				if err := x.h.Run(context.Background(), r.Command, registry.Args(r.Args)); err != nil {
					x.h.Notify(core.Notice{ID: "editor.command", Severity: core.SevError, Source: "editor", Key: true, Text: err.Error()})
				}
			}
		}
		if !isVim(st) && len(in.Keys) > 0 {
			x.e.Follow(p.Editor().Cursors()[0].Off)
		}
		waiting := isVim(st) && len(x.e.Pending()) > 0 || !isVim(st) && st.Pending() != ""
		switch {
		case !waiting:
			x.pendingSince = time.Time{}
		case x.pendingSince.IsZero():
			x.pendingSince = c.Now()
		}
	}
	if r, ok := c.Rect(codeID); ok && !presented {
		// The rows the window has for text: what blocks between its lines took last frame doesn't show text, so the
		// engine keeps the cursor clear of them.
		x.rows[id] = max(int((r.Dy()-kit.CodePadT-x.blockPx[id])/m.LineH), 1)
		x.e.SetRows(id, x.rows[id])
		x.e.SetCols(id, max(int((r.Dx()-gutterWidth(p.Buf().Lines(), m)-4)/m.CharW), 1))
		x.pointer(c, p, st, in, r, m)
	}
	if in.Scroll.Y != 0 {
		x.e.ScrollTo(id, p.Top()+int(math.Round(float64(in.Scroll.Y/m.LineH))))
	}
	if in.Scroll.X != 0 {
		x.e.ScrollLeft(id, p.Left()+int(math.Round(float64(in.Scroll.X/m.CharW))))
	}
	p, _ = x.e.Pane(id) // what the keys left
	c.Column("pane", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(kit.TEditorBg), Clip: true}, func(c *ui.Ctx) {
		x.breadcrumb(c, p, wp)
		if presented {
			x.presentPane(c, p, wp, pr, 0)
			return
		}
		x.drawing = st
		c.Column("code", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{T: kit.CodePadT}, Clip: true, Focusable: true, DefaultFocus: true,
			KeyMode: st.Mode().KeyMode, Text: st.Mode().Text, Wheel: true, Cursor: render.CursorText, Help: "editor/pane", Role: ui.RoleTextBox,
			Label: p.Name()}, func(c *ui.Ctx) {
			x.lines(c, p, m)
		})
		if line, ok := x.e.Cmdline(); ok && p.Current() {
			x.cmdline(c, line)
		}
	})
}

// paneDrag is a press in a window: where it started, how many clicks it is, and whether it's on the gutter.
type paneDrag struct {
	held, gutter bool
	at           vim.Pos // where the press put the cursor: the Visual area's other end
	pressed      vim.Pos // the character pressed on: a drag starts once the pointer leaves it
	last         time.Time
	lastAt       vim.Pos
	clicks       int
	moved        bool
}

// pointer is the mouse in window p (term.txt *mouse-overview*, 'mousemodel' "popup"): a click puts the cursor, a drag
// selects, a double click selects the word and a triple the line, S-click extends the selection and A-drag selects a
// block. The gutter runs its action instead.
func (x *Editor) pointer(c *ui.Ctx, p vim.Pane, st input.State, in ui.Interaction, r render.Rect, m kit.PaneMetrics) {
	if x.drags == nil {
		x.drags = map[vim.WindowID]*paneDrag{}
	}
	d := x.drags[p.ID()]
	if d == nil {
		d = &paneDrag{}
		x.drags[p.ID()] = d
	}
	if in.Context != nil {
		pos, _ := x.posAt(p, r, *in.Context, m)
		x.contextPress(p, st, pos)
	}
	pressed := in.Held || in.Clicked
	if !pressed {
		d.held = false
		return
	}
	at := in.Pointer()
	pos, line := x.posAt(p, r, at, m)
	if !d.held {
		d.held, d.moved = true, false
		d.gutter = at.X-r.Min.X < gutterWidth(p.Buf().Lines(), m) && x.gutterAction(p, line)
		if d.gutter {
			return
		}
		now := c.Now()
		if now.Sub(d.last) < ui.DoubleClick && pos == d.lastAt {
			d.clicks++
		} else {
			d.clicks = 1
		}
		d.last, d.lastAt, d.pressed = now, pos, pos
		x.press(p, st, d, pos, in.Mods)
	} else if !d.gutter && isVim(st) && (d.moved || pos != d.pressed) && pos != x.e.Cursor() {
		if !d.moved {
			d.moved = true
			if _, ok := x.e.Visual(); !ok {
				mode := 'v'
				if in.Mods&render.ModAlt != 0 {
					mode = 0x16
				}
				x.e.Select(d.at, pos, mode)
				return
			}
		}
		x.e.SetCursor(pos)
	}
	if !in.Held {
		d.held = false
		return
	}
	// Dragging past the top or bottom scrolls, a line a frame.
	switch {
	case at.Y < r.Min.Y+kit.CodePadT:
		x.e.ScrollTo(p.ID(), max(p.Top()-1, 0))
		c.WakeAt(c.Now().Add(30 * time.Millisecond))
	case at.Y > r.Max.Y:
		x.e.ScrollTo(p.ID(), p.Top()+1)
		c.WakeAt(c.Now().Add(30 * time.Millisecond))
	}
}

// press is a press at pos: the cursor goes there, or with a double or triple click the word or line is selected, or
// with Shift the selection reaches to it.
func (x *Editor) press(p vim.Pane, st input.State, d *paneDrag, pos vim.Pos, mods render.Mods) {
	x.e.EnterWindow(p.ID())
	if !isVim(st) {
		x.e.SetCursor(pos)
		return
	}
	start, selecting := x.e.Visual()
	switch {
	case mods&render.ModShift != 0:
		if !selecting {
			start = x.e.Cursor()
		}
		x.e.Select(start, pos, 'v')
		d.at = start
	case d.clicks == 2: // *<2-LeftMouse>*: the word
		x.e.Deselect()
		x.e.SetCursor(pos)
		x.e.TypeMapped([]string{"v", "i", "w"}, true)
		d.at, _ = x.e.Visual()
	case d.clicks >= 3: // *<3-LeftMouse>*: the line
		x.e.Select(pos, pos, 'V')
		d.at = pos
	default:
		if selecting {
			x.e.Deselect()
		}
		x.e.SetCursor(pos)
		d.at = x.e.Cursor()
	}
	d.moved = false
}

// contextPress is a right-click at pos ('mousemodel' "popup_setpos"): the cursor goes there, so the menu acts on what
// was clicked, unless it's inside the selection.
func (x *Editor) contextPress(p vim.Pane, st input.State, pos vim.Pos) {
	if r, ok := visualRange(x.e, p); ok && p.Current() {
		if off := p.Buf().LineStart(pos.Line) + pos.Col; off >= r.Start && off < r.End {
			return
		}
	}
	x.e.EnterWindow(p.ID())
	if isVim(st) {
		x.e.Deselect()
	}
	x.e.SetCursor(pos)
}

// lineTop is where a line was drawn: its first row's top.
type lineTop struct {
	line int
	y    float32
}

// posAt is the buffer position under at, and the line it's on.
func (x *Editor) posAt(p vim.Pane, r render.Rect, at render.Point, m kit.PaneMetrics) (vim.Pos, int) {
	row := int(math.Floor(float64((at.Y - r.Min.Y - kit.CodePadT) / m.LineH)))
	line := p.Top()
	if tops := x.lineTops[p.ID()]; len(tops) > 0 && tops[0].line == line {
		i := max(sort.Search(len(tops), func(i int) bool { return tops[i].y > at.Y })-1, 0)
		line, row = tops[i].line, int(math.Floor(float64((at.Y-tops[i].y)/m.LineH)))
	} else {
		for row > 0 && line < p.Buf().Lines()-1 {
			n := p.RowsOf(line)
			if row < n {
				break
			}
			row -= n
			line = p.NextRow(line)
		}
	}
	line = min(line, p.Buf().Lines()-1)
	g := rowSegs(p, line, m)
	seg := g[min(max(row, 0), len(g)-1)]
	want := int(math.Floor(float64((at.X-r.Min.X-m.TextX)/m.CharW))) + seg.from
	if p.Wrap() {
		want = min(want, seg.to-1)
	}
	return vim.Pos{Line: line, Col: colAtVCol(p.Buf().Line(line), max(want, 0), p.TabStop())}, line
}

// gutterAction runs the gutter's action for line, if a plugin gives the file one.
func (x *Editor) gutterAction(p vim.Pane, line int) bool {
	path := x.path(p)
	a, ok := registry.GutterActionFor(x.h, path)
	if !ok {
		return false
	}
	args := registry.Args{"file": path, "line": line + 1}
	for k, v := range a.Args {
		args[k] = v
	}
	if err := x.h.Run(context.Background(), a.Command, args); err != nil {
		x.h.Notify(core.Notice{ID: "editor.gutter", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
	return true
}

func (x *Editor) breadcrumb(c *ui.Ctx, p vim.Pane, wp *windowPresent) {
	th := c.Theme()
	name := kit.TildePath(p.Name())
	if name == "" {
		name = "[No Name]"
	}
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(name), "/"), "/")
	// With several projects the breadcrumb starts with the file's project.
	if file := p.Name(); file != "" {
		if !filepath.IsAbs(file) {
			file = filepath.Join(x.dir, file)
		}
		if w := kit.ProjectsHere(x.h); w.Many() {
			if pr, ok := w.Of(file); ok {
				if rel, err := filepath.Rel(pr.Dir, file); err == nil {
					parts = append([]string{pr.Name}, strings.Split(filepath.ToSlash(rel), "/")...)
				}
			}
		}
	}
	style := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(tCrumbFg)}
	c.Row("crumb", ui.Style{W: ui.Flex(1), H: ui.Px(kit.CrumbH), Pad: ui.PadXY(14, 0), Gap: 6, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
		for i, part := range parts {
			if i > 0 {
				c.Text(fmt.Sprint("sep", i), "›", style)
			}
			s := style
			if i == len(parts)-1 {
				s.Color = th.C(tCrumbLast)
			}
			c.Text(i, part, s)
		}
		if ro := p.ReadOnly(); ro != "" {
			c.Text("ro", "read-only · "+ro, ui.TextStyle{Font: style.Font, Color: th.C(theme.Warn)})
		}
		path := x.path(p)
		if acts := registry.EditorActions(x.h, path); len(acts) > 0 {
			w := registry.EditorWindow{ID: strconv.Itoa(int(p.ID())), Path: path}
			if wp != nil {
				w.Presenter = wp.name
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			for _, a := range acts {
				x.action(c, a, w)
			}
		}
	})
}

// action is an editor action's chip: its icon (drawn, when it's a built-in one), label and note, lit while it's on.
// A click runs its command in the window.
func (x *Editor) action(c *ui.Ctx, a registry.EditorAction, w registry.EditorWindow) {
	on, ic, label := a.Shown(w)
	note := ""
	if a.Note != nil {
		registry.Call(func() error { note = a.Note(w); return nil })
	}
	o := widget.ToggleChipOpts{Help: cmp.Or(a.Help, "editor/pane")}
	switch {
	case kit.HasIcon(ic):
		o.Lead = func(c *ui.Ctx, fg render.Color) { kit.Icon(c, x.h, "icon", ic, 11, fg) }
	case ic != "":
		label = strings.TrimSpace(ic + " " + label)
	}
	o.Label = strings.TrimSpace(cmp.Or(label, a.Name) + " " + note)
	label = strings.TrimSpace(label + " " + note)
	if widget.ToggleChip(c, "act-"+a.Name, label, on, o) == on {
		return
	}
	args := registry.Args{"window": w.ID}
	for k, v := range a.Args {
		args[k] = v
	}
	if err := x.h.Run(context.Background(), a.Command, args); err != nil {
		x.h.Notify(core.Notice{ID: "editor.action", Severity: core.SevWarning, Key: true, Text: err.Error()})
	}
}

// lines draws the rows the window shows: the gutter, the text in its highlights, a closed fold as one row,
// the Visual selection and the cursor.
func (x *Editor) lines(c *ui.Ctx, p vim.Pane, m kit.PaneMetrics) {
	th := c.Theme()
	code, haveCode := c.Rect(c.ID())
	var blockPx float32
	tops := x.lineTops[p.ID()][:0]
	revealTo := -1 // the top line that would show a block that wants revealing whole
	snap := p.Buf()
	rows := x.rows[p.ID()]
	if rows == 0 {
		rows = 40
	}
	line := p.Top()
	last := line
	for range rows {
		if last >= snap.Lines() {
			break
		}
		last = p.NextRow(last)
	}
	var hl []syntax.Span
	if h := x.highlights(p); h.doc != nil {
		end := snap.Len()
		if last < snap.Lines() {
			end = snap.LineStart(last)
		}
		hl, _ = h.doc.Highlights(snap.LineStart(min(line, snap.Lines()-1)), end)
	}
	decor := x.decorations(p, line, last, c.WakeAt)
	cur := p.Cursor()
	gw := gutterWidth(snap.Lines(), m)
	numStyle := ui.TextStyle{Font: x.pane.Font, Color: th.C(kit.TGutterFg)}
	for row := 0; row < rows && line < snap.Lines(); {
		rowID := c.Next(line)
		if r, ok := c.Rect(rowID); ok {
			tops = append(tops, lineTop{line, r.Min.Y})
		}
		foldEnd, folded := p.Fold(line)
		ds := decor[line]
		bg, edge := lineBg(th, ds)
		segs := rowSegs(p, line, m)
		cross := ui.Center
		if len(segs) > 1 {
			cross = ui.Start
		}
		c.Row(line, ui.Style{W: ui.Flex(1), H: ui.Px(m.LineH * float32(len(segs))), Cross: cross, Fill: bg}, func(c *ui.Ctx) {
			ns := numStyle
			if line == cur.Line {
				ns.Color = th.C(tGutterCur)
			}
			if edge.A > 0 {
				ns.Color = theme.WithAlpha(edge, .75)
			}
			num := fmt.Sprint(line + 1)
			c.Row("gutter", ui.Style{W: ui.Px(gw), H: ui.Px(m.LineH), Pad: ui.Insets{R: 16}, Main: ui.End, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", num, ns)
			})
			x.signs(c, ds, gw, m)
			if edge.A > 0 {
				c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(m.LineH * float32(len(segs))), Fill: edge, Float: &ui.Float{Offset: render.Point{X: gw - 2}}}, nil)
			}
			c.Box("gap", ui.Style{W: ui.Px(4)}, nil)
			if !folded {
				x.selections(c, p, line, gw+4, m, ds, segs)
			}
			if folded {
				x.foldRow(c, snap, line, foldEnd)
				return
			}
			if len(segs) == 1 {
				x.textRow(c, snap, line, hl, p.TabStop(), inlays(ds), segs[0].from, segs[0].to)
				virtualText(c, th, ds)
				return
			}
			c.Column("wrap", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				for r, g := range segs {
					c.Row(r, ui.Style{H: ui.Px(m.LineH), Cross: ui.Center}, func(c *ui.Ctx) {
						x.textRow(c, snap, line, hl, p.TabStop(), inlays(ds), g.from, g.to)
						if r == len(segs)-1 {
							virtualText(c, th, ds)
						}
					})
				}
			})
		})
		spans(c, th, p, line, ds, rowID, gw+4, m, segs)
		x.overlay(c, p, rowID, line, folded, gw, m, ds, segs)
		ids, reveal := blocks(c, line, ds, gw+4)
		for i, id := range ids {
			r, ok := c.Rect(id)
			if !ok {
				continue
			}
			blockPx += r.Dy()
			if reveal[i] && haveCode && r.Max.Y > code.Max.Y {
				over := int(math.Ceil(float64((r.Max.Y - code.Max.Y) / m.LineH)))
				revealTo = max(revealTo, min(p.Top()+over, line))
			}
		}
		row += len(segs)
		line = p.NextRow(line)
	}
	x.blockPx[p.ID()], x.lineTops[p.ID()] = blockPx, tops
	if revealTo > p.Top() {
		x.e.ScrollTo(p.ID(), revealTo)
		x.h.Redraw()
	}
}

// rowSeg is one screen row of a line: the screen columns it shows, [from, to), and how far down the line's rows it is.
type rowSeg struct {
	from, to int
	y        float32
}

// rowSegs are line's screen rows in window p: one per row of the pane's width with 'wrap', else one row of the
// columns in view from the window's left column.
func rowSegs(p vim.Pane, line int, m kit.PaneMetrics) []rowSeg {
	if !p.Wrap() {
		to := math.MaxInt32
		if cols := p.Cols(); cols > 0 {
			to = p.Left() + cols + 1 // and the column the edge cuts
		}
		return []rowSeg{{from: p.Left(), to: to}}
	}
	n, cols := p.RowsOf(line), p.Cols()
	out := make([]rowSeg, n)
	for r := range n {
		out[r] = rowSeg{from: r * cols, to: (r + 1) * cols, y: float32(r) * m.LineH}
	}
	return out
}

// clip is screen columns [a, z) on segment g, as an x across the text and a width; false when none of it shows there.
func (g rowSeg) clip(a, z int, m kit.PaneMetrics) (float32, float32, bool) {
	a, z = max(a, g.from), min(z, g.to)
	if z <= a {
		return 0, 0, false
	}
	return float32(a-g.from) * m.CharW, float32(z-a) * m.CharW, true
}

// gutterWidth is the width of a window's gutter: its line numbers and signs.
func gutterWidth(lines int, m kit.PaneMetrics) float32 {
	digits := max(len(fmt.Sprint(lines)), 3)
	return max(float32(kit.GutterW), float32(digits)*m.CharW+34)
}

// textRow draws a line's text in runs of one highlight each, tabs as the spaces up to the next stop, over screen
// columns [left, right): one row of a wrapped line, or the window scrolled sideways with 'nowrap'.
func (x *Editor) textRow(c *ui.Ctx, snap text.Snapshot, line int, hl []syntax.Span, ts int, inl []uireg.Decoration, left, right int) {
	th := c.Theme()
	b := snap.Line(line)
	start := snap.LineStart(line)
	pos := 0
	vcol := 0
	next := 0 // the next inlay to draw
	var run func(key any, s []byte, capture string)
	run = func(key any, s []byte, capture string) {
		if len(s) == 0 {
			return
		}
		if next < len(inl) && inl[next].From < pos+len(s) {
			cut := max(inl[next].From-pos, 0)
			if cut > 0 {
				run(fmt.Sprint(key, "a"), s[:cut], capture)
			}
			if vcol >= left && vcol < right {
				inlayBox(c, fmt.Sprint("inlay", next), inl[next], x.pane)
			}
			next++
			run(fmt.Sprint(key, "b"), s[cut:], capture)
			return
		}
		var out strings.Builder
		for _, r := range string(s) {
			w := 1
			if r == '\t' {
				w = ts - vcol%ts
			}
			switch {
			case vcol >= right:
			case vcol >= left && r == '\t':
				out.WriteString(strings.Repeat(" ", min(vcol+w, right)-vcol))
			case vcol >= left:
				out.WriteRune(r)
			case vcol+w > left: // a tab across the left edge shows what's past it
				out.WriteString(strings.Repeat(" ", min(vcol+w, right)-left))
			}
			vcol += w
		}
		col := th.C(kit.TCodeFg)
		st := ui.TextStyle{Font: x.pane.Font, Color: col}
		if capture != "" {
			s := th.Syntax("@" + capture)
			st.Color = s.FG
			if s.Italic {
				st.Font.Italic = true
			}
			if s.Bold {
				st.Font.Weight = typeset.Bold
			}
		}
		if out.Len() > 0 {
			c.Text(key, out.String(), st)
		}
		pos += len(s)
	}
	k := 0
	for _, h := range hl {
		s, e := h.Range.Start-start, h.Range.End-start
		if e <= pos || s >= len(b) {
			continue
		}
		s, e = max(s, pos), min(e, len(b))
		run(k, b[pos:s], "")
		run(k+1, b[s:e], h.Capture)
		k += 2
		pos = e
	}
	run(k, b[pos:], "")
	for ; next < len(inl); next++ {
		if vcol >= left && vcol < right {
			inlayBox(c, fmt.Sprint("inlay", next), inl[next], x.pane)
		}
	}
}

func (x *Editor) foldRow(c *ui.Ctx, snap text.Snapshot, line, end int) {
	th := c.Theme()
	first := strings.TrimSpace(strings.ReplaceAll(string(snap.Line(line)), "\t", " "))
	c.Row("fold", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tFoldBg), Cross: ui.Center, Gap: 10}, func(c *ui.Ctx) {
		c.Text("text", "▸ "+first, ui.TextStyle{Font: x.pane.Font, Color: th.C(tFoldFg)})
		c.Text("n", fmt.Sprintf("· %d lines", end-line+1), ui.TextStyle{Font: x.pane.Font, Color: th.C(kit.TGutterFg)})
	})
}

// selections draws a row's selection, and each multi-cursor region on it, under its text.
func (x *Editor) selections(c *ui.Ctx, p vim.Pane, line int, x0 float32, m kit.PaneMetrics, ds []uireg.Decoration, segs []rowSeg) {
	fill := c.Theme().C(theme.Selection)
	box := func(key any, from, to, extra int) {
		a, z := p.VCol(line, from)+inlayShift(ds, from, true), p.VCol(line, to)+inlayShift(ds, to, false)+extra
		for r, g := range segs {
			if dx, w, ok := g.clip(a, z, m); ok {
				c.Box(fmt.Sprint(key, r), ui.Style{W: ui.Px(w), H: ui.Px(m.LineH), Fill: fill, Z: ui.LayerUnder,
					Float: &ui.Float{Offset: render.Point{X: x0 + dx, Y: g.y}}}, nil)
			}
		}
	}
	from, to, eol, sel := p.Selected(line)
	if st := x.drawing; st != nil && !isVim(st) {
		from, to, eol, sel = engineSelection(p, line)
	}
	if sel {
		extra := 0
		if eol {
			extra = 1
		}
		box("sel", from, to, extra)
	}
	for i, r := range x.otherCursors(p, line) {
		if r.selFrom >= 0 {
			box(fmt.Sprint("msel", i), r.selFrom, r.selTo, 0)
		}
	}
}

// overlay draws, on the cursor's row of the current window, the cursor over its text, and the other cursors.
func (x *Editor) overlay(c *ui.Ctx, p vim.Pane, rowID ui.ID, line int, folded bool, gw float32, m kit.PaneMetrics, ds []uireg.Decoration, segs []rowSeg) {
	th := c.Theme()
	cur := p.Cursor()
	showCursor := p.Current() && line == cur.Line && x.e.Mode() != "cmdline"
	st := x.drawing
	shape := x.e.CursorShape()
	if st != nil && !isVim(st) {
		shape = map[input.Shape]vim.Shape{input.Block: vim.Block, input.Bar: vim.Bar, input.Underline: vim.Underline}[st.Mode().Shape]
	}
	if folded && p.Current() {
		if end, _ := p.Fold(line); cur.Line >= line && cur.Line <= end {
			showCursor, cur = true, vim.Pos{Line: line}
		}
	}
	others := x.otherCursors(p, line)
	if !showCursor && len(others) == 0 {
		return
	}
	b := p.Buf().Line(line)
	x0 := gw + 4
	// at is where screen column vc is on the line: its segment's x and y, or false when it doesn't show.
	at := func(vc int) (float32, float32, bool) {
		for i, g := range segs {
			if vc >= g.from && (vc < g.to || i == len(segs)-1 && p.Wrap()) {
				return x0 + float32(vc-g.from)*m.CharW, g.y, true
			}
		}
		return 0, 0, false
	}
	var glide cursorGlide
	if showCursor {
		vc := p.VCol(line, cur.Col) + inlayShift(ds, cur.Col, true)
		if cx, cy, ok := at(vc); ok {
			glide = x.glideCursor(c, p, rowID, line, vc, cx, cy, shape, m)
		}
	}
	c.Box(fmt.Sprint("over", line), ui.Style{W: ui.Flex(1), H: ui.Px(m.LineH * float32(len(segs))), Z: ui.LayerContent + 2, Float: &ui.Float{To: rowID}}, func(c *ui.Ctx) {
		for i, r := range others {
			if r.head >= 0 && !(showCursor && r.head == cur.Col) {
				cx, cy, ok := at(p.VCol(line, r.head) + inlayShift(ds, r.head, true))
				if !ok {
					continue
				}
				c.Box(fmt.Sprint("mcur", i), ui.Style{W: ui.Px(m.CharW), H: ui.Px(m.LineH - 2), Fill: theme.WithAlpha(th.C(tCursorBg), .55),
					Float: &ui.Float{To: rowID, Offset: render.Point{X: cx, Y: cy + 1}}, Role: ui.RoleText, Label: "cursor"}, nil)
			}
		}
		if !showCursor {
			return
		}
		cx, cy, ok := at(p.VCol(line, cur.Col) + inlayShift(ds, cur.Col, true))
		if !ok {
			return
		}
		r, size := utf8.DecodeRune(b[min(cur.Col, len(b)):])
		w := float32(1)
		if size > 0 && r != '\t' && wideRune(r) {
			w = 2
		}
		ch := ""
		if size > 0 && r != '\t' {
			ch = string(r)
		}
		st := ui.Style{W: ui.Px(w * m.CharW), H: ui.Px(m.LineH - 2), Fill: th.C(tCursorBg), Float: &ui.Float{To: rowID, Offset: render.Point{X: cx, Y: cy + 1}},
			Role: ui.RoleText, Label: "cursor"}
		switch shape {
		case vim.Bar:
			st.W, ch = ui.Px(2), ""
		case vim.Underline:
			st.H, st.Float.Offset.Y, ch = ui.Px(2), cy+m.LineH-3, ""
		}
		if glide.on {
			st.W, st.H = ui.Px(glide.w(st.W.Value)), ui.Px(glide.h(st.H.Value))
			st.Float.Offset.X += glide.dx
			st.Float.Offset.Y += glide.dy
			if glide.moving {
				ch = "" // its character shows once it's there
			}
		}
		if p.Current() {
			x.caret = c.Next("cursor")
		}
		c.Row("cursor", st, func(c *ui.Ctx) {
			if ch != "" {
				c.Text("ch", ch, ui.TextStyle{Font: x.pane.Font, Color: th.C(tCursorFg)})
			}
		})
	})
}

// engineSelection is another engine's selection on line, from the window's editor's cursors.
func engineSelection(p vim.Pane, line int) (from, to int, eol, ok bool) {
	snap := p.Buf()
	for _, c := range p.Editor().Cursors() {
		r, sel := c.Selection()
		if !sel {
			continue
		}
		a, b := snap.PosAt(r.Start), snap.PosAt(r.End)
		if line < a.Line || line > b.Line {
			continue
		}
		from, to = 0, len(snap.Line(line))
		if line == a.Line {
			from = a.Col
		}
		if line == b.Line {
			to = b.Col
		} else {
			eol = true
		}
		return from, to, eol, true
	}
	return 0, 0, false, false
}

func wideRune(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff ||
		r >= 0xfe30 && r <= 0xfe4f || r >= 0xff00 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 || r >= 0x1f300 && r <= 0x1faff ||
		r >= 0x20000 && r <= 0x3fffd)
}

// colAtVCol is the byte column of the character at screen column want, tabs expanded.
func colAtVCol(b []byte, want, ts int) int {
	vc := 0
	for i, r := range string(b) {
		w := 1
		if r == '\t' {
			w = ts - vc%ts
		} else if wideRune(r) {
			w = 2
		}
		if vc+w > want {
			return i
		}
		vc += w
	}
	return len(b)
}

// cmdline is the ":" line as an IDE input along the bottom of the pane, <Tab> completing it.
func (x *Editor) cmdline(c *ui.Ctx, line string) {
	th := c.Theme()
	c.Row("cmdline", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Gap: 0, Cross: ui.Center, Fill: th.C(tCmdlineBg),
		Stroke: th.C(theme.LineStrong), Role: ui.RoleTextBox, Label: "Command line", Value: line}, func(c *ui.Ctx) {
		c.Text("text", line, ui.TextStyle{Font: x.pane.Font, Color: th.C(kit.TCodeFg)})
		c.Box("caret", ui.Style{W: ui.Px(2), H: ui.Px(16), Fill: th.C(tCursorBg)}, nil)
		if hints := registry.CmdlineHints(x.h, line); len(hints) > 0 {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("hints", strings.Join(hints, " · "), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
		}
	})
}
