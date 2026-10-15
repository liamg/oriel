package app

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// The editor column of 01-ide: a 26 px breadcrumb, then lines of editor.font_size mono (13.5 px, 21 px apart by
// default), with a 64 px gutter.
const (
	crumbH   = 26
	codeSize = 13.5
	codePadT = 6
	gutterW  = 64
)

var (
	tEditorBg     = theme.Declare("editor.bg", "bg.base")
	tCrumbFg      = theme.Declare("editor.breadcrumb.fg", "fg.faint")
	tCrumbLast    = theme.Declare("editor.breadcrumb.current", "fg.muted")
	tGutterFg     = theme.Declare("editor.gutter.fg", "fg.ghost")
	tGutterCur    = theme.Declare("editor.gutter.current", "fg.faint")
	tCodeFg       = theme.Declare("editor.fg", "fg")
	tCursorBg     = theme.Declare("editor.cursor", "accent")
	tCursorFg     = theme.Declare("editor.cursor.fg", "accent.ink")
	tFoldBg       = theme.DeclareOn("editor.fold.bg", "alpha(fg, 5%)", "bg.base")
	tFoldFg       = theme.Declare("editor.fold.fg", "fg.faint")
	tCmdlineBg    = theme.Declare("editor.cmdline.bg", "bg.raised")
	tModeFg       = theme.Declare("statusline.mode.fg", "accent.ink")
	tModeNormal   = theme.Declare("statusline.mode.normal", "accent")
	tModeInsert   = theme.Declare("statusline.mode.insert", "status.quiet")
	tModeVisual   = theme.Declare("statusline.mode.visual", "status.reviewer")
	tModeCommand  = theme.Declare("statusline.mode.command", "status.working")
	tStatusStrong = theme.Declare("statusline.fg.strong", "fg")
)

// codeFont is code at the default size, for code shown outside the editor (help, search).
var codeFont = typeset.FontSpec{Family: typeset.Mono, Size: codeSize}

// paneMetrics are where things are in a pane, for the layout's neighbour finding and the pointer.
type paneMetrics struct {
	charW, lineH, codeTop, textX float32
	font                         typeset.FontSpec // the code's, from editor.font_size
}

// metrics are the panes' for editor.font_size and editor.line_height now.
func (x *editorExt) metrics(c *ui.Ctx) paneMetrics {
	size, lh := float32(codeSize), float32(1.55)
	o := x.h.Options()
	if v, _, err := o.Get("editor.font_size", registry.At{}); err == nil {
		size = float32(v.(float64))
	}
	if v, _, err := o.Get("editor.line_height", registry.At{}); err == nil {
		lh = float32(v.(float64))
	}
	if x.pane.charW == 0 || x.pane.font.Size != size || x.pane.lineH != float32(math.Round(float64(size*lh))) {
		font := typeset.FontSpec{Family: typeset.Mono, Size: size}
		x.pane = paneMetrics{charW: c.Shaper().Shape("M", font).Advance, lineH: float32(math.Round(float64(size * lh))),
			codeTop: crumbH + codePadT, textX: gutterW + 4, font: font}
	}
	return x.pane
}

// paneView draws window id: its breadcrumb, then its lines from the window's top, and the cursor in the
// current window. Keys typed at it go to the engine after the keymap registry passes on them.
func (x *editorExt) paneView(c *ui.Ctx, id vim.WindowID, focused bool) {
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
			r := st.Key(notation(k))
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
	if focused && !presented && !c.FocusWithin(codeID) {
		c.Focus(codeID) // unless a block between its lines has it: a thread's reply
	}
	if r, ok := c.Rect(codeID); ok && !presented {
		// The rows the window has for text: what blocks between its lines took last frame doesn't show text, so the
		// engine keeps the cursor clear of them.
		x.rows[id] = max(int((r.Dy()-codePadT-x.blockPx[id])/m.lineH), 1)
		x.e.SetRows(id, x.rows[id])
		if in.Clicked {
			x.click(p, r, in.Pointer, m)
		}
	}
	if in.Scroll.Y != 0 {
		x.e.ScrollTo(id, p.Top()+int(math.Round(float64(in.Scroll.Y/m.lineH))))
	}
	p, _ = x.e.Pane(id) // what the keys left
	c.Column("pane", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tEditorBg), Clip: true}, func(c *ui.Ctx) {
		x.breadcrumb(c, p, wp)
		if presented {
			x.presentPane(c, p, wp, pr, focused, 0)
			return
		}
		x.drawing = st
		c.Column("code", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{T: codePadT}, Clip: true, Focusable: true,
			KeyMode: st.Mode().KeyMode, Text: st.Mode().Text, Wheel: true, Cursor: render.CursorText, Help: "editor/pane", Role: ui.RoleTextBox,
			Label: p.Name()}, func(c *ui.Ctx) {
			x.lines(c, p, m)
		})
		if line, ok := x.e.Cmdline(); ok && p.Current() {
			x.cmdline(c, line)
		}
	})
}

// click puts the cursor where the pointer is.
func (x *editorExt) click(p vim.Pane, r render.Rect, at render.Point, m paneMetrics) {
	row := int((at.Y - r.Min.Y - codePadT) / m.lineH)
	line := p.Top()
	for ; row > 0 && line < p.Buf().Lines()-1; row-- {
		line = p.NextRow(line)
	}
	line = min(line, p.Buf().Lines()-1)
	want := int((at.X - r.Min.X - m.textX + m.charW/2) / m.charW)
	x.e.EnterWindow(p.ID())
	x.e.SetCursor(vim.Pos{Line: line, Col: colAtVCol(p.Buf().Line(line), max(want, 0), p.TabStop())})
}

func (x *editorExt) breadcrumb(c *ui.Ctx, p vim.Pane, wp *windowPresent) {
	th := c.Theme()
	name := tildePath(p.Name())
	if name == "" {
		name = "[No Name]"
	}
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(name), "/"), "/")
	// With several projects the breadcrumb starts with the file's project.
	if file := p.Name(); file != "" {
		if !filepath.IsAbs(file) {
			file = filepath.Join(x.dir, file)
		}
		if w := projectsHere(x.h); w.many() {
			if pr, ok := w.of(file); ok {
				if rel, err := filepath.Rel(pr.Dir, file); err == nil {
					parts = append([]string{pr.Name}, strings.Split(filepath.ToSlash(rel), "/")...)
				}
			}
		}
	}
	style := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(tCrumbFg)}
	c.Row("crumb", ui.Style{W: ui.Flex(1), H: ui.Px(crumbH), Pad: ui.PadXY(14, 0), Gap: 6, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
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
func (x *editorExt) action(c *ui.Ctx, a registry.EditorAction, w registry.EditorWindow) {
	on, ic, label := a.Shown(w)
	note := ""
	if a.Note != nil {
		registry.Call(func() error { note = a.Note(w); return nil })
	}
	o := widget.ToggleChipOpts{Help: cmpOr(a.Help, "editor/pane")}
	switch {
	case icons[ic] != nil:
		o.Lead = func(c *ui.Ctx, fg render.Color) { icon(c, "icon", ic, 11, fg) }
	case ic != "":
		label = strings.TrimSpace(ic + " " + label)
	}
	o.Label = strings.TrimSpace(cmpOr(label, a.Name) + " " + note)
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
func (x *editorExt) lines(c *ui.Ctx, p vim.Pane, m paneMetrics) {
	th := c.Theme()
	code, haveCode := c.Rect(c.ID())
	var blockPx float32
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
	decor := x.decorations(p, line, last)
	cur := p.Cursor()
	digits := max(len(fmt.Sprint(snap.Lines())), 3)
	gw := max(float32(gutterW), float32(digits)*m.charW+34)
	numStyle := ui.TextStyle{Font: x.pane.font, Color: th.C(tGutterFg)}
	for row := 0; row < rows && line < snap.Lines(); row++ {
		rowID := c.Next(line)
		foldEnd, folded := p.Fold(line)
		ds := decor[line]
		bg, edge := lineBg(th, ds)
		c.Row(line, ui.Style{W: ui.Flex(1), H: ui.Px(m.lineH), Cross: ui.Center, Fill: bg}, func(c *ui.Ctx) {
			ns := numStyle
			if line == cur.Line {
				ns.Color = th.C(tGutterCur)
			}
			if edge.A > 0 {
				ns.Color = theme.WithAlpha(edge, .75)
			}
			num := fmt.Sprint(line + 1)
			c.Row("gutter", ui.Style{W: ui.Px(gw), Pad: ui.Insets{R: 16}, Main: ui.End}, func(c *ui.Ctx) {
				c.Text("n", num, ns)
			})
			x.signs(c, ds, gw, m)
			if edge.A > 0 {
				c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(m.lineH), Fill: edge, Float: &ui.Float{Offset: render.Point{X: gw - 2}}}, nil)
			}
			c.Box("gap", ui.Style{W: ui.Px(4)}, nil)
			if !folded {
				x.selections(c, p, line, gw+4, m, ds)
			}
			if folded {
				x.foldRow(c, snap, line, foldEnd)
				return
			}
			x.textRow(c, snap, line, hl, p.TabStop(), inlays(ds))
			virtualText(c, th, ds)
		})
		spans(c, th, p, line, ds, rowID, gw+4, m)
		x.overlay(c, p, rowID, line, folded, gw, m, ds)
		ids, reveal := blocks(c, line, ds, gw+4)
		for i, id := range ids {
			r, ok := c.Rect(id)
			if !ok {
				continue
			}
			blockPx += r.Dy()
			if reveal[i] && haveCode && r.Max.Y > code.Max.Y {
				over := int(math.Ceil(float64((r.Max.Y - code.Max.Y) / m.lineH)))
				revealTo = max(revealTo, min(p.Top()+over, line))
			}
		}
		line = p.NextRow(line)
	}
	x.blockPx[p.ID()] = blockPx
	if revealTo > p.Top() {
		x.e.ScrollTo(p.ID(), revealTo)
		x.h.Redraw()
	}
}

// textRow draws a line's text in runs of one highlight each, tabs as the spaces up to the next stop.
func (x *editorExt) textRow(c *ui.Ctx, snap text.Snapshot, line int, hl []syntax.Span, ts int, inl []registry.Decoration) {
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
			inlayBox(c, fmt.Sprint("inlay", next), inl[next], x.pane)
			next++
			run(fmt.Sprint(key, "b"), s[cut:], capture)
			return
		}
		var out strings.Builder
		for _, r := range string(s) {
			if r == '\t' {
				n := ts - vcol%ts
				out.WriteString(strings.Repeat(" ", n))
				vcol += n
				continue
			}
			out.WriteRune(r)
			vcol++
		}
		col := th.C(tCodeFg)
		st := ui.TextStyle{Font: x.pane.font, Color: col}
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
		c.Text(key, out.String(), st)
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
		inlayBox(c, fmt.Sprint("inlay", next), inl[next], x.pane)
	}
}

func (x *editorExt) foldRow(c *ui.Ctx, snap text.Snapshot, line, end int) {
	th := c.Theme()
	first := strings.TrimSpace(strings.ReplaceAll(string(snap.Line(line)), "\t", " "))
	c.Row("fold", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tFoldBg), Cross: ui.Center, Gap: 10}, func(c *ui.Ctx) {
		c.Text("text", "▸ "+first, ui.TextStyle{Font: x.pane.font, Color: th.C(tFoldFg)})
		c.Text("n", fmt.Sprintf("· %d lines", end-line+1), ui.TextStyle{Font: x.pane.font, Color: th.C(tGutterFg)})
	})
}

// selections draws a row's selection, and each multi-cursor region on it, under its text.
func (x *editorExt) selections(c *ui.Ctx, p vim.Pane, line int, x0 float32, m paneMetrics, ds []registry.Decoration) {
	fill := c.Theme().C(theme.Selection)
	box := func(key any, from, to, extra int) {
		a, z := p.VCol(line, from)+inlayShift(ds, from, true), p.VCol(line, to)+inlayShift(ds, to, false)+extra
		c.Box(key, ui.Style{W: ui.Px(max(float32(z-a), 0) * m.charW), H: ui.Px(m.lineH), Fill: fill, Z: ui.LayerUnder,
			Float: &ui.Float{Offset: render.Point{X: x0 + float32(a)*m.charW}}}, nil)
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
func (x *editorExt) overlay(c *ui.Ctx, p vim.Pane, rowID ui.ID, line int, folded bool, gw float32, m paneMetrics, ds []registry.Decoration) {
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
	c.Box(fmt.Sprint("over", line), ui.Style{W: ui.Flex(1), H: ui.Px(m.lineH), Z: ui.LayerContent + 2, Float: &ui.Float{To: rowID}}, func(c *ui.Ctx) {
		for i, r := range others {
			if r.head >= 0 && !(showCursor && r.head == cur.Col) {
				vc := p.VCol(line, r.head) + inlayShift(ds, r.head, true)
				c.Box(fmt.Sprint("mcur", i), ui.Style{W: ui.Px(m.charW), H: ui.Px(m.lineH - 2), Fill: theme.WithAlpha(th.C(tCursorBg), .55),
					Float: &ui.Float{To: rowID, Offset: render.Point{X: x0 + float32(vc)*m.charW, Y: 1}}, Role: ui.RoleText, Label: "cursor"}, nil)
			}
		}
		if !showCursor {
			return
		}
		vc := p.VCol(line, cur.Col) + inlayShift(ds, cur.Col, true)
		r, size := utf8.DecodeRune(b[min(cur.Col, len(b)):])
		w := float32(1)
		if size > 0 && r != '\t' && wideRune(r) {
			w = 2
		}
		ch := ""
		if size > 0 && r != '\t' {
			ch = string(r)
		}
		cx := x0 + float32(vc)*m.charW
		st := ui.Style{W: ui.Px(w * m.charW), H: ui.Px(m.lineH - 2), Fill: th.C(tCursorBg), Float: &ui.Float{To: rowID, Offset: render.Point{X: cx, Y: 1}},
			Role: ui.RoleText, Label: "cursor"}
		switch shape {
		case vim.Bar:
			st.W, ch = ui.Px(2), ""
		case vim.Underline:
			st.H, st.Float.Offset.Y, ch = ui.Px(2), m.lineH-3, ""
		}
		if p.Current() {
			x.caret = c.Next("cursor")
		}
		c.Row("cursor", st, func(c *ui.Ctx) {
			if ch != "" {
				c.Text("ch", ch, ui.TextStyle{Font: x.pane.font, Color: th.C(tCursorFg)})
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
func (x *editorExt) cmdline(c *ui.Ctx, line string) {
	th := c.Theme()
	c.Row("cmdline", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Gap: 0, Cross: ui.Center, Fill: th.C(tCmdlineBg),
		Stroke: th.C(theme.LineStrong), Role: ui.RoleTextBox, Label: "Command line", Value: line}, func(c *ui.Ctx) {
		c.Text("text", line, ui.TextStyle{Font: x.pane.font, Color: th.C(tCodeFg)})
		c.Box("caret", ui.Style{W: ui.Px(2), H: ui.Px(16), Fill: th.C(tCursorBg)}, nil)
		if hints := registry.CmdlineHints(x.h, line); len(hints) > 0 {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("hints", strings.Join(hints, " · "), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
		}
	})
}

// modeSegment is the status line's mode chip: the current window's engine and its mode.
func (x *editorExt) modeSegment(c *ui.Ctx) {
	th := c.Theme()
	if f := x.h.Layout().Focus; f != nil && f.ID != EditorView {
		if v, ok := x.h.ViewByID(f.ID); ok {
			if m, ok := registry.As[registry.Moded](v); ok {
				label, style := m.ModeLabel(f.Instance)
				c.Row("mode", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: tokenColour(th, style, th.C(tModeNormal)),
					Role: ui.RoleStatus, Label: "Mode"}, func(c *ui.Ctx) {
					c.Text("m", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Bold, Tracking: .12},
						Color: th.C(tModeFg)})
				})
				return
			}
		}
	}
	if !x.shown() {
		return
	}
	mode := x.e.Mode()
	label := modeName(mode)
	if st := x.inputFor(x.current()); !isVim(st) {
		mode, label = "insert", st.Mode().Label
	}
	if _, pr, ok := x.presenting(x.current()); ok {
		mode, label = "", strings.ToUpper(cmpOr(pr.Label, pr.Name))
	}
	bg := map[string]theme.TokenID{"insert": tModeInsert, "replace": tModeInsert, "visual": tModeVisual, "visual-line": tModeVisual,
		"visual-block": tModeVisual, "cmdline": tModeCommand}[mode]
	if bg == 0 {
		bg = tModeNormal
	}
	c.Row("modes", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("mode", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: th.C(bg), Role: ui.RoleStatus, Label: "Mode"}, func(c *ui.Ctx) {
			c.Text("m", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Bold, Tracking: .12},
				Color: th.C(tModeFg)})
		})
		if m, ok := x.e.Multi(); ok {
			c.Row("multi", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(10, 0), Cross: ui.Center, Fill: th.C(tModeVisual), Role: ui.RoleStatus, Label: "Cursors"},
				func(c *ui.Ctx) {
					c.Text("n", fmt.Sprintf("%d CURSORS", len(m.Regions)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5,
						Weight: typeset.Bold, Tracking: .12}, Color: th.C(tModeFg)})
				})
		}
	})
}

// shown reports whether any editor window is in the layout.
func (x *editorExt) shown() bool {
	for _, id := range x.e.WindowIDs() {
		if x.h.Layout().Find(windowRef(id)) != nil {
			return true
		}
	}
	return false
}

func (x *editorExt) current() vim.Pane {
	p, _ := x.e.Pane(x.e.WindowID())
	return p
}

// fileSegment is the file's language, encoding and line endings: "Go · utf-8 · LF".
func (x *editorExt) fileSegment(c *ui.Ctx) {
	if !x.shown() {
		return
	}
	p := x.current()
	parts := []string{}
	if h, ok := x.docs[p.Buffer()]; ok && h.lang != "" {
		parts = append(parts, strings.ToUpper(h.lang[:1])+h.lang[1:])
	}
	enc, eol := "utf-8", "LF"
	if f := x.e.File(); f != nil {
		if f.Format.BOM {
			enc = "utf-8 bom"
		}
		if f.Format.CRLF {
			eol = "CRLF"
		}
	}
	parts = append(parts, enc, eol)
	c.Text("file", strings.Join(parts, " · "), statusText(c, tStatusFg))
}

// positionSegment is the cursor's line and column, and how far through the file it is.
func (x *editorExt) positionSegment(c *ui.Ctx) {
	if !x.shown() {
		return
	}
	p := x.current()
	cur := p.Cursor()
	n := p.Buf().Lines()
	c.Row("pos", ui.Style{Gap: 16, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("lc", fmt.Sprintf("%d:%d", cur.Line+1, p.VCol(cur.Line, cur.Col)+1), statusText(c, tStatusFg))
		c.Text("pct", fmt.Sprintf("≡ %d%%", (cur.Line+1)*100/max(n, 1)), statusText(c, tStatusFg))
	})
}

func statusText(c *ui.Ctx, tok theme.TokenID) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Medium}, Color: c.Theme().C(tok)}
}

// lineCursor is one of the multi-cursors on a line: its head's column (-1 if it isn't on the line), and the part of
// its region the line holds (-1 for none).
type lineCursor struct{ head, selFrom, selTo int }

// otherCursors are the cursors of a multi-cursor edit on line, in the current window.
func (x *editorExt) otherCursors(p vim.Pane, line int) []lineCursor {
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
