package lang

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tTermDotOK  = theme.Declare("terminal.exit.ok", "ok")
	tTermDotBad = theme.Declare("terminal.exit.failed", "error")
	tTermMode   = theme.Declare("statusline.mode.terminal", "status.quiet")
	tTermSel    = theme.DeclareOn("terminal.selection", "alpha(accent, 28%)", "terminal.bg")
)

const (
	termFontSize = 12.5
	termLineH    = 17
	termGutter   = 14 // the exit dots
)

var termFont = typeset.FontSpec{Family: typeset.Mono, Size: termFontSize}

type terminalView struct{ x *Terminals }

func (v terminalView) EdgeToEdge() {}

// termState is a terminal as the layout keeps it across restarts: what it ran; its screen is the Terminals' own.
type termState struct {
	ID  string   `json:"id"`
	Cmd []string `json:"cmd,omitempty"`
	// Sandboxed revives it as a new shell in the session's sandbox: the old one's command reached a container that's gone.
	Sandboxed bool `json:"sandboxed,omitempty"`
}

func (v terminalView) Persist(instance string) (json.RawMessage, bool) {
	t := v.x.get(instance)
	if t == nil || !t.own {
		return nil, false
	}
	st := termState{ID: instance, Cmd: t.cmd, Sandboxed: t.sandboxed}
	if t.sandboxed {
		st.Cmd = nil
	}
	b, _ := json.Marshal(st)
	return b, true
}

// Revive is the terminal running still, the window back on its directory; else it starts the program again, under
// its screen from before.
func (v terminalView) Revive(state json.RawMessage) (string, error) {
	var s termState
	if err := json.Unmarshal(state, &s); err != nil {
		return "", err
	}
	if s.ID != "" && v.x.get(s.ID) != nil {
		return s.ID, nil
	}
	a := registry.Args{"cmd": s.Cmd, "instance": s.ID}
	if w, se, ok := v.x.sandboxed(a); ok {
		c, err := w.SandboxShell(context.Background(), se, kit.WindowDir(v.x.h))
		if err != nil {
			return "", err
		}
		return v.x.start(a, &c)
	}
	return v.x.start(a, nil)
}

func (v terminalView) Title(instance string) string {
	if t := v.x.get(instance); t != nil {
		return t.title()
	}
	return "terminal"
}

// ModeLabel is the status line's mode chip while a terminal has focus.
func (v terminalView) ModeLabel(instance string) (string, string) {
	if t := v.x.get(instance); t != nil && t.normal {
		return "TERM-NORMAL", tTermMode.Name()
	}
	return "TERMINAL", tTermMode.Name()
}

func (v terminalView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "terminal", Fn: func(c *ui.Ctx) {
		t := v.x.get(vc.Instance)
		if t == nil {
			c.Text("gone", "this terminal has closed", ui.TextStyle{})
			return
		}
		if t.rename.On {
			c.Column("renamed", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Row("rename", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("k", "Name", kit.MonoFont(10.5, c.Theme().C(theme.FgFaint)))
					if name, done := widget.RenameField(c, "name", &t.rename, widget.InputOpts{Label: "Terminal name", Width: ui.Px(320),
						Help: "terminal"}); done {
						t.name = name
					}
				})
				v.x.draw(c, t)
			})
			return
		}
		v.x.draw(c, t)
	}}
}

// draw is the terminal: keys to the program (or to vim in terminal-normal mode), resized to its pane, its grid drawn
// in one paint, and the exit dots of its commands down the left.
func (x *Terminals) draw(c *ui.Ctx, t *terminal) {
	th := c.Theme()
	charW := c.Shaper().Shape("M", termFont).Advance
	id := c.Next("term")
	in := c.Interaction(id)
	for _, k := range in.Keys {
		x.key(c, t, k)
	}
	if in.Scroll.Y != 0 && !t.normal {
		x.wheel(c, t, in, id, charW)
	}
	if r, ok := c.Rect(id); ok {
		cols, rows := max(int((r.Dx()-termGutter-8)/charW), 2), max(int((r.Dy()-6)/termLineH), 2)
		if cols != t.cols || rows != t.rows {
			t.cols, t.rows = cols, rows
			t.emu.Resize(cols, rows)
			t.src.Resize(cols, rows)
		}
		if !t.normal {
			x.pointer(c, t, in, r, charW)
		}
	}
	mode := TerminalKeyMode
	if t.normal {
		mode = TerminalNormalKeyMode
	}
	c.Box("term", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(kit.TTermBg), Clip: true, Focusable: true, DefaultFocus: true, KeyMode: mode, Wheel: true,
		Cursor: render.CursorText, Help: "terminal", Role: ui.RoleTextBox, Label: t.title()}, func(c *ui.Ctx) {
		if t.normal {
			x.drawNormal(c, t, charW)
			return
		}
		grid := t.emu.Grid()
		cs := t.emu.CursorState()
		sb := t.emu.Scrollbar()
		dots := t.exitDots()
		c.Paint("grid", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			x0 := r.Min.X + termGutter
			from, to, selecting := t.selection()
			for row, line := range grid.Lines {
				y := r.Min.Y + 3 + float32(row)*termLineH
				if at := sb.Offset + row; selecting && at >= from.Y && at <= to.Y {
					lo, hi := 0, len(line.Cells)
					if at == from.Y {
						lo = from.X
					}
					if at == to.Y {
						hi = to.X + 1
					}
					if hi > lo {
						f.Rect(render.Rect{Min: render.Point{X: x0 + float32(lo)*charW, Y: y}, Max: render.Point{X: x0 + float32(hi)*charW, Y: y + termLineH}},
							render.Style{Fill: th.C(tTermSel)})
					}
				}
				if d, ok := dots[sb.Offset+row]; ok {
					tok := tTermDotOK
					if d == "failed" {
						tok = tTermDotBad
					}
					if d != "running" {
						f.Rect(render.Rect{Min: render.Point{X: r.Min.X + 4, Y: y + 6}, Max: render.Point{X: r.Min.X + 9, Y: y + 11}},
							render.Style{Fill: th.C(tok), Radius: 2.5})
					}
				}
				drawTermLine(f, c.Shaper(), th, line.Cells, x0, y, charW)
			}
			if cs.InViewport && cs.Visible {
				cx, cy := x0+float32(cs.ViewX)*charW, r.Min.Y+3+float32(cs.ViewY)*termLineH
				rc := render.Rect{Min: render.Point{X: cx, Y: cy}, Max: render.Point{X: cx + charW, Y: cy + termLineH}}
				switch cs.Shape {
				case term.ShapeBar:
					rc.Max.X = cx + 2
				case term.ShapeUnderline:
					rc.Min.Y = cy + termLineH - 2
				}
				f.Rect(rc, render.Style{Fill: theme.WithAlpha(th.C(kit.TTermCursor), .85)})
			}
		})
		if t.done {
			c.Box("exited", ui.Style{Pad: ui.PadXY(8, 2), Radius: 4, Fill: th.C(theme.BgRaised), Float: &ui.Float{At: render.Point{X: 1, Y: 1},
				Self: render.Point{X: 1, Y: 1}, Offset: render.Point{X: -8, Y: -6}}}, func(c *ui.Ctx) {
				c.Text("t", "process exited", ui.TextStyle{Font: termFont, Color: th.C(theme.FgFaint)})
			})
		}
	})
}

// drawTermLine draws a row of cells in the terminal's font.
func drawTermLine(f render.Frame, sh typeset.Shaper, th *theme.Resolved, cells []term.Cell, x0, y, charW float32) {
	kit.DrawTermLine(f, sh, th, cells, x0, y, charW, termFont, termLineH)
}

// drawNormal is terminal-normal mode: the scrollback as vim's window shows it, with its cursor and Visual area.
func (x *Terminals) drawNormal(c *ui.Ctx, t *terminal, charW float32) {
	th := c.Theme()
	p, ok := t.vim.Pane(t.vim.WindowID())
	if !ok {
		return
	}
	t.vim.SetRows(p.ID(), t.rows)
	snap := p.Buf()
	top := p.Top()
	cur := p.Cursor()
	dots := t.exitDots()
	c.Paint("lines", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
		x0 := r.Min.X + termGutter
		for row := 0; row < t.rows && top+row < snap.Lines(); row++ {
			line := top + row
			y := r.Min.Y + 3 + float32(row)*termLineH
			if from, to, eol, sel := p.Selected(line); sel {
				a, z := p.VCol(line, from), p.VCol(line, to)
				if eol {
					z++
				}
				f.Rect(render.Rect{Min: render.Point{X: x0 + float32(a)*charW, Y: y}, Max: render.Point{X: x0 + float32(z)*charW, Y: y + termLineH}},
					render.Style{Fill: th.C(theme.Selection)})
			}
			if d, ok := dots[line]; ok && d != "running" {
				tok := tTermDotOK
				if d == "failed" {
					tok = tTermDotBad
				}
				f.Rect(render.Rect{Min: render.Point{X: r.Min.X + 4, Y: y + 6}, Max: render.Point{X: r.Min.X + 9, Y: y + 11}},
					render.Style{Fill: th.C(tok), Radius: 2.5})
			}
			if line < len(t.cells) && t.cells[line] != nil {
				drawTermLine(f, c.Shaper(), th, t.cells[line], x0, y, charW)
			} else if s := string(snap.Line(line)); s != "" {
				g := c.Shaper().Shape(s, termFont)
				f.Glyphs(g, render.Point{X: x0, Y: y + (termLineH+g.Ascent-g.Descent)/2}, th.C(kit.TTermFg))
			}
			if line == cur.Line {
				cx := x0 + float32(p.VCol(line, cur.Col))*charW
				f.Rect(render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + charW, Y: y + termLineH}},
					render.Style{Fill: theme.WithAlpha(th.C(kit.TTermCursor), .7)})
			}
		}
	})
}

// key handles a key at the terminal: in terminal mode it's the program's (⌃\ ⌃N goes to terminal-normal mode);
// in terminal-normal mode it's vim's, [[ and ]] jump between prompts, and i, a, I or A go back.
func (x *Terminals) key(c *ui.Ctx, t *terminal, k render.KeyEvent) {
	n := kit.Notation(k)
	if t.normal {
		if t.bracket != "" {
			b := t.bracket
			t.bracket = ""
			if n == b {
				if b == "[" {
					t.jumpPrompt(-1)
				} else {
					t.jumpPrompt(1)
				}
				return
			}
			t.vim.Key(b)
		}
		if t.vim.Mode() == "normal" && len(t.vim.Pending()) == 0 {
			switch n {
			case "i", "a", "I", "A":
				t.normal, t.vim, t.ed, t.cells = false, nil, nil, nil
				t.emu.Scroll(term.Scroll{To: term.ScrollBottom})
				return
			case "[", "]":
				t.bracket = n
				return
			}
		}
		t.vim.Key(n)
		return
	}
	if t.done {
		return
	}
	if n == "<D-a>" {
		sb := t.emu.Scrollbar()
		t.sel = &termSel{anchor: term.Point{}, end: term.Point{X: max(t.cols-1, 0), Y: max(sb.Total-1, 0)}, clicks: 2}
		return
	}
	if n == "<D-c>" || n == "<C-S-c>" {
		if from, to, ok := t.selection(); ok {
			c.Clipboard().SetClipboard(t.emu.Text(from, to))
			t.sel = nil
			return
		}
	}
	t.sel = nil
	if n == "<D-v>" || n == "<C-S-v>" {
		if s, ok := c.Clipboard().ReadClipboard(); ok {
			t.src.Write(t.emu.Paste(s))
		}
		return
	}
	if b := t.emu.EncodeKey(termKey(k)); len(b) > 0 {
		t.emu.Scroll(term.Scroll{To: term.ScrollBottom})
		t.src.Write(b)
	}
}

// wheel takes the wheel's movement in whole notches: to the program when it asks for them (term.Wheel), else, or
// with Shift held, over the scrollback.
func (x *Terminals) wheel(c *ui.Ctx, t *terminal, in ui.Interaction, id ui.ID, charW float32) {
	t.wheel += in.Scroll.Y / termLineH
	n := int(t.wheel)
	t.wheel -= float32(n)
	if n == 0 {
		return
	}
	if r, ok := c.Rect(id); ok && !t.done && c.Held()&render.ModShift == 0 {
		at := in.Pointer()
		col := min(max(int((at.X-r.Min.X-termGutter)/charW), 0), t.cols-1)
		row := min(max(int((at.Y-r.Min.Y-3)/termLineH), 0), t.rows-1)
		if b := term.Wheel(t.emu, col, row, n); b != nil {
			t.emu.Scroll(term.Scroll{To: term.ScrollBottom})
			t.src.Write(b)
			return
		}
	}
	t.emu.Scroll(term.Scroll{To: term.ScrollDelta, Delta: n})
}

// termKeys are the named keys, as render names them, and the physical keys they are.
var termKeys = map[string]term.Key{
	"CR": term.KeyEnter, "Tab": term.KeyTab, "BS": term.KeyBackspace, "Esc": term.KeyEscape, "Del": term.KeyDelete,
	"Up": term.KeyArrowUp, "Down": term.KeyArrowDown, "Left": term.KeyArrowLeft, "Right": term.KeyArrowRight,
	"Home": term.KeyHome, "End": term.KeyEnd, "PageUp": term.KeyPageUp, "PageDown": term.KeyPageDown, "Insert": term.KeyInsert,
	"F1": term.KeyF1, "F2": term.KeyF2, "F3": term.KeyF3, "F4": term.KeyF4, "F5": term.KeyF5, "F6": term.KeyF6,
	"F7": term.KeyF7, "F8": term.KeyF8, "F9": term.KeyF9, "F10": term.KeyF10, "F11": term.KeyF11, "F12": term.KeyF12,
}

// termKey is a key press as the VT's encoder takes it: a physical key, its mods, and the text it types.
func termKey(k render.KeyEvent) term.KeyEvent {
	ev := term.KeyEvent{Action: term.KeyPress}
	if k.Mods&render.ModShift != 0 {
		ev.Mods |= term.ModShift
	}
	if k.Mods&render.ModCtrl != 0 {
		ev.Mods |= term.ModCtrl
	}
	if k.Mods&render.ModAlt != 0 {
		ev.Mods |= term.ModAlt
	}
	if k.Mods&render.ModSuper != 0 {
		ev.Mods |= term.ModSuper
	}
	if key, ok := termKeys[k.Key]; ok {
		ev.Key = key
		return ev
	}
	r, size := utf8.DecodeRuneInString(k.Key)
	if size != len(k.Key) {
		return ev
	}
	lower := r
	if r >= 'A' && r <= 'Z' {
		lower = r + 'a' - 'A'
	}
	switch {
	case lower >= 'a' && lower <= 'z':
		ev.Key = term.KeyA + term.Key(lower-'a')
	case r >= '0' && r <= '9':
		ev.Key = term.KeyDigit0 + term.Key(r-'0')
	case r == ' ':
		ev.Key = term.KeySpace
	}
	ev.Unshifted = lower
	if ev.Mods&(term.ModCtrl|term.ModAlt|term.ModSuper) == 0 {
		ev.Text = k.Key
	}
	return ev
}

// termSel is a selection in a terminal, by cell from the top of its scrollback, so it holds while the view scrolls.
type termSel struct {
	anchor, end term.Point
	held        bool
	clicks      int
	last        time.Time
	lastAt      term.Point
}

// selection is t's selected cells, first to last, while it has some.
func (t *terminal) selection() (from, to term.Point, ok bool) {
	s := t.sel
	if s == nil || s.anchor == s.end && s.clicks < 2 {
		return term.Point{}, term.Point{}, false
	}
	from, to = s.anchor, s.end
	if to.Y < from.Y || to.Y == from.Y && to.X < from.X {
		from, to = to, from
	}
	return from, to, true
}

// pointer selects in t with the mouse: a drag from the press, a double click the word, a triple the row. Dragging past
// the top or bottom scrolls.
func (x *Terminals) pointer(c *ui.Ctx, t *terminal, in ui.Interaction, r render.Rect, charW float32) {
	if !in.Held && !in.Clicked {
		if t.sel != nil {
			t.sel.held = false
		}
		return
	}
	at := in.Pointer()
	sb := t.emu.Scrollbar()
	row := int(math.Floor(float64((at.Y - r.Min.Y - 3) / termLineH)))
	switch {
	case in.Held && row < 0:
		t.emu.Scroll(term.Scroll{To: term.ScrollDelta, Delta: -1})
		c.WakeAt(c.Now().Add(40 * time.Millisecond))
	case in.Held && row >= t.rows:
		t.emu.Scroll(term.Scroll{To: term.ScrollDelta, Delta: 1})
		c.WakeAt(c.Now().Add(40 * time.Millisecond))
	}
	cell := func(at render.Point) term.Point {
		row := int(math.Floor(float64((at.Y - r.Min.Y - 3) / termLineH)))
		col := min(max(int(math.Floor(float64((at.X-r.Min.X-termGutter)/charW))), 0), t.cols-1)
		return term.Point{X: col, Y: sb.Offset + min(max(row, 0), t.rows-1)}
	}
	p := cell(at)
	s := t.sel
	if s != nil && s.held {
		if s.clicks < 2 {
			s.end = p
		}
		s.held = in.Held
		return
	}
	// A new selection starts where the press went down: the pointer may have moved on before this frame was built.
	from := p
	if pt, ok := in.PressedAt(); ok {
		from = cell(pt)
	}
	now := c.Now()
	clicks := 1
	if s != nil && now.Sub(s.last) < ui.DoubleClick && from == s.lastAt {
		clicks = s.clicks + 1
	}
	s = &termSel{anchor: from, end: p, held: in.Held, clicks: clicks, last: now, lastAt: from}
	t.sel = s
	if clicks == 1 {
		return
	}
	line := t.emu.Grid().Lines[p.Y-sb.Offset].Cells
	if clicks >= 3 {
		s.anchor.X, s.end.X = 0, max(len(line)-1, 0)
		return
	}
	blank := func(i int) bool { return i < 0 || i >= len(line) || strings.TrimSpace(line[i].Text) == "" }
	if blank(p.X) {
		return
	}
	for s.anchor.X > 0 && !blank(s.anchor.X-1) {
		s.anchor.X--
	}
	for !blank(s.end.X + 1) {
		s.end.X++
	}
}
