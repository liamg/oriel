package widget

import (
	"math"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type InputOpts struct {
	Placeholder string
	Width       ui.Size // grows to fill by default
	Disabled    bool
	Help        string
}

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

// Input is a one-line text field (base.css .input) editing *text. Keys: typing inserts, Backspace and Delete
// delete, Left and Right move (with Shift to select), Home and End jump, Enter submits, Escape cancels.
// A click puts the cursor where it lands.
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
			shift := k.Mods&render.ModShift != 0
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
			switch {
			case k.Key == "CR":
				res.Submitted = true
			case k.Key == "Esc":
				res.Cancelled = true
				c.Blur()
			case k.Key == "Left":
				move(st.cursor - 1)
			case k.Key == "Right":
				move(st.cursor + 1)
			case k.Key == "Home":
				move(0)
			case k.Key == "End":
				move(len(runes))
			case k.Key == "BS":
				if lo == hi && lo > 0 {
					lo--
				}
				cut()
			case k.Key == "Del":
				if lo == hi && hi < len(runes) {
					hi++
				}
				cut()
			case k.Mods&^render.ModShift == 0 && utf8.RuneCountInString(k.Key) == 1:
				cut()
				r, _ := utf8.DecodeRuneInString(k.Key)
				runes = append(runes[:lo:lo], append([]rune{r}, runes[lo:]...)...)
				st.cursor, st.anchor = lo+1, lo+1
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
	if in.Focused {
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
		Radius: 6, Clip: true, Focusable: true, Disabled: o.Disabled, Cursor: render.CursorText, Help: o.Help, KeyMode: "input"}, func(rc *ui.Ctx) {
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

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
