package vim

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/edit"

	"github.com/liamg/oriel/text"
)

// Pane is one window as the IDE draws it. It reads the engine as it is now, so use it on the goroutine that
// feeds the engine keys, and don't keep it past the frame.
type Pane struct {
	e *Engine
	w *window
}

// Pane is window id, if it's open.
func (e *Engine) Pane(id WindowID) (Pane, bool) {
	w := e.windowByID(id)
	return Pane{e, w}, w != nil
}

func (p Pane) ID() WindowID         { return WindowID(p.w.id) }
func (p Pane) Current() bool        { return p.w == p.e.window }
func (p Pane) Buf() text.Snapshot   { return p.w.buf.text.Snapshot() }
func (p Pane) Buffer() *text.Buffer { return p.w.buf.text }
func (p Pane) Name() string         { return p.w.buf.name }
func (p Pane) Modified() bool       { return p.w.buf.modified }
func (p Pane) Top() int             { return p.w.top }

// ReadOnly says why the buffer can't be written, or "".
func (p Pane) ReadOnly() string {
	if p.w.buf.file == nil {
		return ""
	}
	return p.w.buf.file.ReadOnly
}

// Cursor is the window's cursor.
func (p Pane) Cursor() Pos {
	if p.Current() {
		return p.e.cur
	}
	return p.w.cur
}

func (p Pane) view() *view {
	v := newView(p.Buf())
	v.folds = &p.w.fold
	return v
}

// Fold is the closed fold starting at line: its last line, or false when line doesn't start one.
func (p Pane) Fold(line int) (int, bool) {
	start, end, ok := p.w.fold.closedAt(line)
	return end, ok && start == line
}

// NextRow is the first line of the row after line's: past a closed fold.
func (p Pane) NextRow(line int) int {
	_, end, _ := p.w.fold.closedAt(line)
	return end + 1
}

// Selected is the Visual selection on line, as byte columns [from, to); eol when it takes in the line break.
func (p Pane) Selected(line int) (from, to int, eol, ok bool) {
	e := p.e
	if !p.Current() || e.visual == nil {
		return 0, 0, false, false
	}
	v := p.view()
	a, c := *e.visual, e.cur
	if less(c, a) {
		a, c = c, a
	}
	if line < a.Line || line > c.Line {
		return 0, 0, false, false
	}
	n := v.lineLen(line)
	switch e.vmode {
	case 'V':
		return 0, n, true, true
	case ctrlV:
		left, right := e.blockCols(v)
		if e.want == maxCol {
			right = maxCol
		}
		from, to, ok := lineSpan(v, line, left, right)
		return from, to, false, ok
	}
	from, to = 0, n
	if line == a.Line {
		from = a.Col
	}
	if line == c.Line {
		_, size := v.char(c)
		to = c.Col + max(size, 1)
	}
	return from, min(to, n), to > n, true
}

// VCol is the screen column where the character at col starts, tabs expanded.
func (p Pane) VCol(line, col int) int { return p.view().vcol(line, col) }

// Shape is how the cursor looks in the engine's mode: a block, a bar between characters, or an underline.
type Shape int

const (
	Block Shape = iota
	Bar
	Underline
)

func (e *Engine) CursorShape() Shape {
	switch {
	case e.mode == Insert:
		return Bar
	case e.mode == Replace:
		return Underline
	case e.Mode() == "op-pending":
		return Underline
	}
	return Block
}

// SetRows tells a window how many rows its pane shows.
func (e *Engine) SetRows(id WindowID, rows int) {
	w := e.windowByID(id)
	if w == nil || w.height == rows {
		return
	}
	w.height = rows
	if w == e.window {
		e.updateTop(e.view())
	}
}

// ScrollTo shows line first in window id, as the scroll wheel does; the cursor stays on the window.
func (e *Engine) ScrollTo(id WindowID, line int) {
	if w := e.windowByID(id); w != nil {
		prev := e.window
		e.window, e.buffer = w, w.buf
		e.SetTop(line)
		e.window, e.buffer = prev, prev.buf
	}
}

// NewWindow opens a window on the file name ("" for an empty buffer) without placing it: the IDE has put a view
// for it in its layout, or is about to. It becomes the current window.
func (e *Engine) NewWindow(name string) (WindowID, error) {
	b := e.emptyBuffer()
	if name != "" {
		var err error
		if b, err = e.openFile(name); err != nil {
			return 0, err
		}
	}
	w := e.newWindow(e.ed.Open(b.text), b)
	w.cur = b.last
	e.enter(w)
	e.sync()
	return WindowID(w.id), nil
}

// EnterWindow makes window id current, as clicking its pane does.
func (e *Engine) EnterWindow(id WindowID) {
	if w := e.windowByID(id); w != nil && w != e.window {
		e.enter(w)
		e.sync()
	}
}

// WindowClosed forgets window id after the IDE closed its pane; the next window in the layout becomes current.
func (e *Engine) WindowClosed(id WindowID) {
	w := e.windowByID(id)
	if w == nil || len(e.windows) == 1 {
		return
	}
	e.windows = slices.DeleteFunc(e.windows, func(x *window) bool { return x == w })
	if w == e.window {
		next := e.windows[0]
		if o := e.order(); len(o) > 0 {
			next = o[0]
		}
		e.window = nil
		e.enter(next)
		e.sync()
	}
	if e.prevWin == w {
		e.prevWin = nil
	}
}

// Windows is every window, in the layout's order, then any the layout doesn't show.
func (e *Engine) WindowIDs() []WindowID {
	var out []WindowID
	for _, w := range e.order() {
		out = append(out, WindowID(w.id))
	}
	for _, w := range e.windows {
		if !slices.Contains(out, WindowID(w.id)) {
			out = append(out, WindowID(w.id))
		}
	}
	return out
}

// Cmdline is the command line being typed, with its first character (":", "/", "?"), and whether there is one.
func (e *Engine) Cmdline() (string, bool) {
	if e.cmdline == nil {
		return "", false
	}
	return string(e.cmdline.kind) + e.cmdline.text, true
}

// SetCmdline replaces what's typed on the command line, as completion does.
func (e *Engine) SetCmdline(s string) {
	if e.cmdline != nil {
		e.cmdline.text = s
	}
}

// ExNames are the ex commands the engine knows, by full name, for completion.
func ExNames() []string {
	out := make([]string, 0, len(exNames))
	for _, c := range exNames {
		out = append(out, c.full)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// BufferInfo is one listed buffer, for the IDE's switcher.
type BufferInfo struct {
	Num               int
	Name              string
	Modified, Current bool
	Alternate         bool
	Line              int // where the cursor was, 0-based
}

// BufferList is the listed buffers, as :ls lists them.
func (e *Engine) BufferList() []BufferInfo {
	var out []BufferInfo
	for _, b := range e.listed() {
		line := b.last.Line
		if b == e.buffer {
			line = e.cur.Line
		}
		out = append(out, BufferInfo{Num: b.num, Name: b.displayName(), Modified: b.modified, Current: b == e.buffer,
			Alternate: b == e.window.alt, Line: line})
	}
	return out
}

// Run runs an ex command line, as typing it after ":" would.
func (e *Engine) Run(line string) {
	e.ex(e.view(), strings.TrimPrefix(line, ":"))
	e.clamp(e.view())
	e.updateTop(e.view())
	e.sync()
}

// QfItem is one entry of the quickfix list, for the IDE's results panel.
type QfItem struct {
	Name      string
	Line, Col int // 1-based; Col 0 when there's none
	Text      string
	Valid     bool
}

// Quickfix is the current quickfix list, its title, and which entry is current.
func (e *Engine) Quickfix() (items []QfItem, title string, current int) {
	l := e.qf.list()
	if l == nil {
		return nil, "", 0
	}
	for _, en := range l.entries {
		items = append(items, QfItem{Name: en.name, Line: en.line, Col: en.col, Text: en.text, Valid: en.valid})
	}
	return items, l.title, l.idx
}

// TabStop is how many columns a tab takes, for drawing text as the engine counts columns.
const TabStop = tabstop

// Editor is the window's editor, for another input engine to edit through.
func (p Pane) Editor() *edit.Editor { return p.w.ed }

// Follow moves the current window's cursor to off, as another input engine left it: anywhere on a line, the end
// included, and scrolls to show it.
func (e *Engine) Follow(off int) {
	v := e.view()
	e.cur = v.pos(min(off, v.s.Len()))
	e.setWant(v)
	e.updateTop(v)
}
