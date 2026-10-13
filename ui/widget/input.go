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
}

// Digits accepts what a whole number is typed with.
func Digits(r rune) bool { return r >= '0' && r <= '9' || r == '-' }

// Decimal accepts what a number is typed with.
func Decimal(r rune) bool { return Digits(r) || r == '.' || r == 'e' || r == 'E' || r == '+' }

type InputResult struct {
	Changed   bool // the text changed this frame
	Submitted bool // Enter
	Cancelled bool // Escape, which also drops focus
}

type inputState struct {
	cursor, anchor int // in runes; a selection runs between them
	scroll         float32
	dragging       bool
}

// Input is a one-line text field (base.css .input) editing *text, with the platform's editing keys (editKey): typing
// inserts, moving by character, word and line (Shift selects), select all, cut, copy and paste, deleting back and
// forward by character, word or line. Enter submits, Escape cancels. A click puts the cursor where it lands.
func Input(c *ui.Ctx, key any, text *string, o InputOpts) InputResult {
	id := c.Next(key)
	in := c.Interaction(id)
	st := ui.State[inputState](c, id)
	font := typeset.FontSpec{Family: typeset.UI, Size: 12.5}
	runes := []rune(*text)
	st.cursor, st.anchor = clampInt(st.cursor, 0, len(runes)), clampInt(st.anchor, 0, len(runes))
	var res InputResult

	prefix := func(n int) float32 { return c.Shaper().Shape(string(runes[:n]), font).Advance }
	if in.Clicked || in.Pressed {
		if r, ok := c.Rect(id); ok {
			x := in.Pointer.X - r.Min.X - 10 + st.scroll
			best, bestD := 0, float32(math.MaxFloat32)
			for i := 0; i <= len(runes); i++ {
				if d := abs32(prefix(i) - x); d < bestD {
					best, bestD = i, d
				}
			}
			st.cursor = best
			// The press anchors a selection (Shift keeps the old anchor); dragging extends it.
			if !st.dragging && in.Mods&render.ModShift == 0 {
				st.anchor = best
			}
		}
	}
	st.dragging = in.Pressed
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
					if r >= ' ' && (o.Accept == nil || o.Accept(r)) {
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
			key := editKey(k.Key, mods)
			switch key {
			case "submit":
				res.Submitted = true
			case "cancel":
				res.Cancelled = true
				c.Blur()
			case "tab":
				if o.TabMoves {
					c.FocusNext(map[bool]int{false: 1, true: -1}[shift])
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
				move(0)
			case "line-end":
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
					lo = map[string]int{"delete-back": max(lo-1, 0), "delete-word-back": wordLeft(runes, lo), "delete-line-back": 0}[key]
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
	lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
	selFrom, selTo, caret := prefix(lo), prefix(hi), prefix(st.cursor)
	focused, scroll := in.Focused, st.scroll
	c.Row(key, ui.Style{W: width, H: ui.Px(30), Pad: ui.PadXY(10, 0), Cross: ui.Center, Fill: col(c, tInputBg), Stroke: border, Border: 1,
		Radius: 6, Clip: true, Focusable: true, Disabled: o.Disabled, Cursor: render.CursorText, Help: o.Help, KeyMode: "input",
		Role: ui.RoleTextBox, Label: o.Label, Value: string(runes)}, func(rc *ui.Ctx) {
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
		case km{"Left", platform.line}, km{"Up", platform.line}:
			return "line-start"
		case km{"Right", platform.line}, km{"Down", platform.line}:
			return "line-end"
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
