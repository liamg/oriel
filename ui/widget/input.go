package widget

import (
	"math"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type InputOpts struct {
	Label       string // its accessible name; the placeholder isn't one
	Placeholder string
	Width       ui.Size // grows to fill by default
	Disabled    bool
	Help        string
	TabMoves    bool              // Tab and ⇧Tab move focus on, rather than going to whoever reads the input's keys
	Accept      func(r rune) bool // what can be typed, when not anything: Digits for a number
	Invalid     bool              // the text isn't valid yet: an error border
	// SelectOnFocus selects all the text each time the input gets focus, so typing replaces it: a rename's name.
	SelectOnFocus bool
	KeyMode       string // its keymap mode, for keys of its own (a composer's ⌘⏎); "input" by default
	// Lines makes it a multi-line field showing up to this many lines: its text wraps, it grows with it and then
	// scrolls; ⏎ is a new line and ⌘⏎ (Ctrl⏎ elsewhere) submits. 0 and 1 are one line.
	Lines    int
	MinLines int // the lines a multi-line field shows at least
}

// Digits accepts what a whole number is typed with.
func Digits(r rune) bool { return r >= '0' && r <= '9' || r == '-' }

// Decimal accepts what a number is typed with.
func Decimal(r rune) bool { return Digits(r) || r == '.' || r == 'e' || r == 'E' || r == '+' }

type InputResult struct {
	Changed   bool // the text changed this frame
	Submitted bool // Enter; ⌘⏎ in a multi-line field
	Cancelled bool // Escape, which also drops focus
}

type inputState struct {
	cursor, anchor int // in runes; a selection runs between them
	scroll         float32
	goal           float32 // a multi-line field's x that Up and Down keep to, while hasGoal
	hasGoal        bool
	width          float32 // the width a multi-line field wrapped at, last frame
	dragging       bool
	focused        bool // last frame
}

// A multi-line field's line height and its padding above and below.
const lineH, linesPadY = 18, 6

// span is a line of a multi-line field as it wraps: the runes [from, to), to before any newline.
type span struct{ from, to int }

// wrapSpans breaks runes into lines that fit width at spaces (a word wider than that anywhere), keeping its
// newlines; width 0 doesn't wrap.
func wrapSpans(runes []rune, width float32, adv func(from, to int) float32) []span {
	var out []span
	from := 0
	for i := 0; i <= len(runes); i++ {
		if i < len(runes) && runes[i] != '\n' {
			continue
		}
		for {
			end := from
			for j := from; j < i; {
				k := j
				for k < i && runes[k] != ' ' {
					k++
				}
				if width > 0 && adv(from, k) > width {
					break
				}
				for k < i && runes[k] == ' ' {
					k++
				}
				end, j = k, k
			}
			if end == from && from < i {
				end = from + 1
				for end < i && adv(from, end+1) <= width {
					end++
				}
			}
			out = append(out, span{from, end})
			if from = end; from >= i {
				break
			}
		}
		from = i + 1
	}
	return out
}

// lineOf is the line the rune offset i is on.
func lineOf(spans []span, i int) int {
	k := 0
	for n, sp := range spans {
		if sp.from <= i {
			k = n
		}
	}
	return k
}

// Input is a text field (base.css .input) editing *text, with the platform's editing keys (editKey): typing
// inserts, moving by character, word and line (Shift selects), select all, cut, copy and paste, deleting back and
// forward by character, word or line. Enter submits, Escape cancels. A click puts the cursor where it lands. With
// Lines it takes several lines (InputOpts.Lines).
func Input(c *ui.Ctx, key any, text *string, o InputOpts) InputResult {
	id := c.Next(key)
	in := c.Interaction(id)
	st := ui.State[inputState](c, id)
	font := typeset.FontSpec{Family: typeset.UI, Size: 12.5}
	multi := o.Lines > 1
	runes := []rune(*text)
	st.cursor, st.anchor = clampInt(st.cursor, 0, len(runes)), clampInt(st.anchor, 0, len(runes))
	var res InputResult
	if o.SelectOnFocus && in.Focused && !st.focused && !in.Clicked && !in.Pressed {
		st.anchor, st.cursor = 0, len(runes)
	}
	st.focused = in.Focused

	advance := func(from, to int) float32 { return c.Shaper().Shape(string(runes[from:to]), font).Advance }
	prefix := func(n int) float32 { return advance(0, n) }
	spans := func() []span {
		if !multi {
			return []span{{0, len(runes)}}
		}
		return wrapSpans(runes, st.width, advance)
	}
	// nearest is the offset on line sp nearest x from the line's start.
	nearest := func(sp span, x float32) int {
		best, bestD := sp.from, float32(math.MaxFloat32)
		for i := sp.from; i <= sp.to; i++ {
			if d := abs32(advance(sp.from, i) - x); d < bestD {
				best, bestD = i, d
			}
		}
		return best
	}
	if in.Clicked || in.Pressed {
		if r, ok := c.Rect(id); ok {
			at := st.cursor
			if multi {
				if tr, ok := c.Rect(ui.Child(id, "text")); ok {
					ls := spans()
					k := clampInt(int((in.Pointer.Y-tr.Min.Y)/lineH), 0, len(ls)-1)
					at = nearest(ls[k], in.Pointer.X-tr.Min.X)
				}
			} else {
				at = nearest(span{0, len(runes)}, in.Pointer.X-r.Min.X-10+st.scroll)
			}
			st.cursor, st.hasGoal = at, false
			// The press anchors a selection (Shift keeps the old anchor); dragging extends it.
			if !st.dragging && in.Mods&render.ModShift == 0 {
				st.anchor = at
			}
		}
	}
	st.dragging = in.Pressed
	moved := in.Clicked || in.Pressed
	if !o.Disabled {
		for _, k := range in.Keys {
			shift, mods := k.Mods&render.ModShift != 0, k.Mods&^render.ModShift
			move := func(to int) {
				st.cursor = clampInt(to, 0, len(runes))
				if !shift {
					st.anchor = st.cursor
				}
			}
			lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
			cut := func() {
				runes = append(runes[:lo:lo], runes[hi:]...)
				st.cursor, st.anchor = lo, lo
				res.Changed = true
			}
			insert := func(s string) {
				var add []rune
				for _, r := range s {
					if (r >= ' ' || multi && r == '\n') && (o.Accept == nil || o.Accept(r)) {
						add = append(add, r)
					}
				}
				if len(add) == 0 {
					return
				}
				cut()
				runes = append(runes[:lo:lo], append(add, runes[lo:]...)...)
				st.cursor, st.anchor = lo+len(add), lo+len(add)
			}
			line := func() span {
				ls := spans()
				return ls[lineOf(ls, st.cursor)]
			}
			key := editKey(k.Key, mods)
			if multi {
				switch {
				case key == "submit":
					key = "newline"
				case k.Key == "CR" && mods == platform.primary:
					key = "submit"
				}
			}
			if key != "up" && key != "down" {
				st.hasGoal = false
			}
			moved = true
			switch key {
			case "submit":
				res.Submitted = true
			case "newline":
				insert("\n")
			case "cancel":
				res.Cancelled = true
				c.Blur()
			case "tab":
				if o.TabMoves {
					c.FocusNext(map[bool]int{false: 1, true: -1}[shift])
				}
			case "up", "down":
				ls := spans()
				n := lineOf(ls, st.cursor)
				if !st.hasGoal {
					st.goal, st.hasGoal = advance(ls[n].from, st.cursor), true
				}
				switch {
				case key == "up" && n == 0:
					move(0)
				case key == "down" && n == len(ls)-1:
					move(len(runes))
				case key == "up":
					move(nearest(ls[n-1], st.goal))
				default:
					move(nearest(ls[n+1], st.goal))
				}
			case "left":
				if lo != hi && !shift {
					move(lo)
				} else {
					move(st.cursor - 1)
				}
			case "right":
				if lo != hi && !shift {
					move(hi)
				} else {
					move(st.cursor + 1)
				}
			case "word-left":
				move(wordLeft(runes, st.cursor))
			case "word-right":
				move(wordRight(runes, st.cursor))
			case "line-start":
				move(line().from)
			case "line-end":
				move(line().to)
			case "text-start":
				move(0)
			case "text-end":
				move(len(runes))
			case "select-all":
				st.anchor, st.cursor = 0, len(runes)
			case "copy", "cut":
				if lo != hi {
					c.Clipboard().SetClipboard(string(runes[lo:hi]))
					if key == "cut" {
						cut()
					}
				}
			case "paste":
				if s, ok := c.Clipboard().ReadClipboard(); ok {
					insert(s)
				}
			case "delete-back", "delete-word-back", "delete-line-back":
				if lo == hi {
					lo = map[string]int{"delete-back": max(lo-1, 0), "delete-word-back": wordLeft(runes, lo), "delete-line-back": line().from}[key]
				}
				cut()
			case "delete", "delete-word":
				if lo == hi {
					hi = map[string]int{"delete": min(hi+1, len(runes)), "delete-word": wordRight(runes, hi)}[key]
				}
				cut()
			default:
				if mods == 0 && utf8.RuneCountInString(k.Key) == 1 {
					insert(k.Key)
				}
			}
		}
	}
	if res.Changed || len(in.Keys) > 0 {
		*text = string(runes)
	}

	width := o.Width
	if width == (ui.Size{}) {
		width = ui.Flex(1)
	}
	border := col(c, tInputBorder)
	switch {
	case o.Invalid:
		border = col(c, tInputInvalid)
	case in.Focused:
		border = col(c, tInputFocus)
	}
	fg := col(c, tInputFg)
	if o.Disabled {
		fg = col(c, tDisabledFg)
	}
	mode := o.KeyMode
	if mode == "" {
		mode = "input"
	}
	lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
	focused := in.Focused
	style := ui.Style{W: width, Fill: col(c, tInputBg), Stroke: border, Border: 1, Radius: 6, Clip: true, Focusable: true, Disabled: o.Disabled,
		Cursor: render.CursorText, Help: o.Help, KeyMode: mode, Text: true, Role: ui.RoleTextBox, Label: o.Label, Value: string(runes)}
	if multi {
		if r, ok := c.Rect(id); ok {
			st.width = r.Dx() - 20
		}
		ls := spans()
		rows := clampInt(len(ls), max(o.MinLines, 1), o.Lines)
		if moved {
			// Keep the caret's line in view.
			top, view := float32(lineOf(ls, st.cursor))*lineH, float32(rows)*lineH
			sc := c.Scroll(id)
			if top < sc.Y {
				sc.Y = top
			}
			if top+lineH > sc.Y+view {
				sc.Y = top + lineH - view
			}
			c.ScrollTo(id, sc)
		}
		style.Dir, style.H, style.Pad, style.ScrollY = ui.Column, ui.Px(float32(rows)*lineH+2*linesPadY), ui.PadXY(10, linesPadY), true
		c.Box(key, style, func(rc *ui.Ctx) {
			rc.Paint("text", ui.Flex(1), ui.Px(float32(len(ls))*lineH), func(f render.Frame, r render.Rect) {
				if len(runes) == 0 && o.Placeholder != "" {
					run := c.Shaper().Shape(o.Placeholder, font)
					f.Glyphs(run, render.Point{X: r.Min.X, Y: r.Min.Y + lineH/2 + (run.Ascent-run.Descent)/2}, col(c, tInputHint))
				}
				for k, sp := range ls {
					y := r.Min.Y + float32(k)*lineH
					if a, b := max(lo, sp.from), min(hi, sp.to); focused && hi > lo && a <= b && lo <= sp.to && hi >= sp.from {
						x0, x1 := advance(sp.from, a), advance(sp.from, b)
						if hi > sp.to && sp.to < len(runes) {
							x1 += 4 // the newline, or the wrap, is selected too
						}
						f.Rect(render.XYWH(r.Min.X+x0, y+1, x1-x0, 16), render.Style{Fill: c.Theme().C(tRowSelected)})
					}
					if sp.to > sp.from {
						run := c.Shaper().Shape(string(runes[sp.from:sp.to]), font)
						f.Glyphs(run, render.Point{X: r.Min.X, Y: y + lineH/2 + (run.Ascent-run.Descent)/2}, fg)
					}
				}
				if focused {
					k := lineOf(ls, st.cursor)
					x := advance(ls[k].from, st.cursor)
					f.Rect(render.XYWH(r.Min.X+x, r.Min.Y+float32(k)*lineH+1, 1, 16), render.Style{Fill: col(c, tInputCaret)})
				}
			})
		})
		return res
	}
	// Keep the caret in view.
	if r, ok := c.Rect(id); ok {
		view := r.Dx() - 20
		cx := prefix(st.cursor)
		if cx-st.scroll > view {
			st.scroll = cx - view
		}
		if cx < st.scroll {
			st.scroll = cx
		}
	}
	selFrom, selTo, caret := prefix(lo), prefix(hi), prefix(st.cursor)
	scroll := st.scroll
	style.H, style.Pad, style.Cross = ui.Px(30), ui.PadXY(10, 0), ui.Center
	c.Row(key, style, func(rc *ui.Ctx) {
		rc.Paint("text", ui.Flex(1), ui.Px(30), func(f render.Frame, r render.Rect) {
			if focused && hi > lo {
				sel := c.Theme().C(tRowSelected)
				f.Rect(render.XYWH(r.Min.X+selFrom-scroll, r.Min.Y+7, selTo-selFrom, 16), render.Style{Fill: sel})
			}
			s, tc := string(runes), fg
			if s == "" {
				s, tc = o.Placeholder, col(c, tInputHint)
			}
			if s != "" {
				run := c.Shaper().Shape(s, font)
				f.Glyphs(run, render.Point{X: r.Min.X - scroll, Y: r.Min.Y + 15 + (run.Ascent-run.Descent)/2}, tc)
			}
			if focused {
				f.Rect(render.XYWH(r.Min.X+caret-scroll, r.Min.Y+7, 1, 16), render.Style{Fill: col(c, tInputCaret)})
			}
		})
	})
	return res
}

// editKey is what a key does in a text field, by the platform's conventions: ⌘ for the line and the clipboard and
// ⌥ for words on macOS, Ctrl elsewhere.
func editKey(key string, mods render.Mods) string {
	type km struct {
		key  string
		mods render.Mods
	}
	k := km{key, mods}
	switch k {
	case km{"CR", 0}:
		return "submit"
	case km{"Esc", 0}:
		return "cancel"
	case km{"Tab", 0}:
		return "tab"
	case km{"Left", 0}:
		return "left"
	case km{"Right", 0}:
		return "right"
	case km{"Up", 0}:
		return "up"
	case km{"Down", 0}:
		return "down"
	case km{"Home", 0}:
		return "line-start"
	case km{"End", 0}:
		return "line-end"
	case km{"BS", 0}:
		return "delete-back"
	case km{"Del", 0}:
		return "delete"
	}
	if k.mods == 0 {
		return ""
	}
	switch k {
	case km{"Left", platform.word}:
		return "word-left"
	case km{"Right", platform.word}:
		return "word-right"
	case km{"BS", platform.word}:
		return "delete-word-back"
	case km{"Del", platform.word}:
		return "delete-word"
	case km{"a", platform.primary}:
		return "select-all"
	case km{"c", platform.primary}:
		return "copy"
	case km{"x", platform.primary}:
		return "cut"
	case km{"v", platform.primary}:
		return "paste"
	}
	if platform.line != 0 {
		switch k {
		case km{"Left", platform.line}:
			return "line-start"
		case km{"Right", platform.line}:
			return "line-end"
		case km{"Up", platform.line}:
			return "text-start"
		case km{"Down", platform.line}:
			return "text-end"
		case km{"BS", platform.line}:
			return "delete-line-back"
		}
	}
	return ""
}

// wordLeft is where the word before i starts, past any spaces; wordRight is where the word after i ends.
func wordLeft(rs []rune, i int) int {
	for i > 0 && !isWord(rs[i-1]) {
		i--
	}
	for i > 0 && isWord(rs[i-1]) {
		i--
	}
	return i
}

func wordRight(rs []rune, i int) int {
	for i < len(rs) && !isWord(rs[i]) {
		i++
	}
	for i < len(rs) && isWord(rs[i]) {
		i++
	}
	return i
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
