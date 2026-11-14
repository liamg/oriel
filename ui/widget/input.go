package widget

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
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
	// EnterSubmits makes a multi-line field a message's (a comment, a reply): ⏎ submits and ⇧⏎ is a new line.
	EnterSubmits bool
	// Secret shows each character as a dot, to the eye and to assistive technology, and won't copy or cut it: a
	// token being pasted in. One line only.
	Secret bool
	// DefaultFocus makes it where focus goes when what it's in opens (ui.Style.DefaultFocus): a dialog's first field.
	DefaultFocus bool
	// Step makes it a number field that assistive technology can increment and decrement by Step.
	Step float64
	// Files takes what a paste offers besides text (copied files, a picture) and files dropped on the field, and
	// returns the text that goes in their place. Without it a paste is text only, and the field takes no drops.
	Files func(Pasted) string
}

// Pasted is what a paste or a drop gives a field besides text.
type Pasted struct {
	Paths []string // files copied in the file manager, or dropped
	Image []byte   // a copied picture, as PNG
}

// pasteFiles is what o.Files makes of the clipboard: copied files, else a picture when there's no text.
func pasteFiles(c *ui.Ctx, o InputOpts) (string, bool) {
	fc, ok := c.Clipboard().(render.ClipboardFiles)
	if o.Files == nil || !ok {
		return "", false
	}
	if paths := fc.ReadClipboardFiles(); len(paths) > 0 {
		return o.Files(Pasted{Paths: paths}), true
	}
	if _, text := c.Clipboard().ReadClipboard(); text {
		return "", false
	}
	if png, ok := fc.ReadClipboardImage(); ok {
		return o.Files(Pasted{Image: png}), true
	}
	return "", false
}

// Digits accepts what a whole number is typed with.
func Digits(r rune) bool { return r >= '0' && r <= '9' || r == '-' }

// Decimal accepts what a number is typed with.
func Decimal(r rune) bool { return Digits(r) || r == '.' || r == 'e' || r == 'E' || r == '+' }

type InputResult struct {
	Changed   bool // the text changed this frame
	Submitted bool // ⏎; in a multi-line field ⌘⏎, and ⏎ with EnterSubmits
	Cancelled bool // Escape, which also drops focus
}

type inputState struct {
	cursor, anchor int // in runes; a selection runs between them
	scroll         float32
	goal           float32 // a multi-line field's x that Up and Down keep to, while hasGoal
	hasGoal        bool
	width          float32 // the width a multi-line field wrapped at, last frame
	dragging       bool
	presses        int  // the press the drag began with: 2 holds a word, 3 a line
	focused        bool // last frame
	undo, redo     []inputEdit
	join           string // what the last edit was ("type", a dictation's), which the next of the same joins for undo
	last           string // the text as the field last left it: when it's changed from outside, undo starts again
	drawn          bool   // it has been drawn before
}

// fieldExtra is what a Prose adds to the field Input edits.
type fieldExtra struct {
	keys   []render.KeyEvent // the keys to edit with, in place of the field's own
	edit   *fieldEdit        // made before the keys, through the edit path typing takes
	muted  [2]int            // runes drawn in fg.muted: a phrase still being heard
	stroke render.Color      // the border, when set
	padR   float32           // room kept on the right, for inlay
	inlay  func(c *ui.Ctx)   // built inside the field's box
}

// fieldEdit puts Insert in place of runes [From, To). Edits with the same Join undo together.
type fieldEdit struct {
	From, To int
	Insert   string
	Join     string
}

// inputEdit is a field as it was, for undo and redo.
type inputEdit struct {
	text           string
	cursor, anchor int
}

// undoMax is how many edits a field can undo.
const undoMax = 100

var (
	textActions   = []ui.Action{ui.ActionSetValue, ui.ActionSelect}
	numberActions = append(slices.Clone(textActions), stepActions...)
)

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
// Lines it takes several lines (InputOpts.Lines). Its selection is painted in input.selection.
func Input(c *ui.Ctx, key any, text *string, o InputOpts) InputResult {
	return field(c, key, text, o, nil)
}

// fieldFrame is a field in one frame: its text as runes while it's edited, and what the frame did to it.
type fieldFrame struct {
	c     *ui.Ctx
	id    ui.ID
	in    ui.Interaction
	st    *inputState
	o     InputOpts
	x     *fieldExtra
	font  typeset.FontSpec
	multi bool
	runes []rune
	res   InputResult
	moved bool // the caret may have moved: keep it in view
}

func field(c *ui.Ctx, key any, text *string, o InputOpts, x *fieldExtra) InputResult {
	if x == nil {
		x = &fieldExtra{}
	}
	id := c.Next(key)
	ff := &fieldFrame{c: c, id: id, in: c.Interaction(id), st: ui.State[inputState](c, id), o: o, x: x,
		font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, multi: o.Lines > 1 && !o.Secret, runes: []rune(*text)}
	ff.sync(*text)
	ff.pointer()
	keys := ff.in.Keys
	if x.keys != nil {
		keys = x.keys
	}
	ff.outside()
	if !o.Disabled {
		for _, k := range keys {
			ff.key(k)
		}
		for _, a := range ff.in.Access {
			ff.access(a)
		}
	}
	if ff.res.Changed || len(keys) > 0 {
		*text = string(ff.runes)
	}
	ff.st.last = *text
	style := ff.style()
	if ff.multi {
		ff.drawLines(key, style)
	} else {
		ff.drawLine(key, style)
	}
	return ff.res
}

// sync brings the field's state up to its text: a text changed from outside starts undo again.
func (ff *fieldFrame) sync(text string) {
	st, in, n := ff.st, ff.in, len(ff.runes)
	st.cursor, st.anchor = clampInt(st.cursor, 0, n), clampInt(st.anchor, 0, n)
	if text != st.last {
		st.undo, st.redo, st.join = nil, nil, ""
		if st.drawn { // text put in from outside since (a message brought back to edit): its caret goes to the end
			st.cursor, st.anchor = n, n
		}
	}
	st.drawn = true
	if ff.o.SelectOnFocus && in.Focused && !st.focused && !in.Clicked && !in.Pressed {
		st.anchor, st.cursor = 0, n
	}
	st.focused = in.Focused
}

// shown is the text as drawn: dots for a secret's.
func (ff *fieldFrame) shown(from, to int) string {
	if ff.o.Secret {
		return strings.Repeat("•", to-from)
	}
	return string(ff.runes[from:to])
}

func (ff *fieldFrame) advance(from, to int) float32 {
	return ff.c.Shaper().Shape(ff.shown(from, to), ff.font).Advance
}

func (ff *fieldFrame) prefix(n int) float32 { return ff.advance(0, n) }

// word is at from i, or end in a secret, whose words aren't told apart.
func (ff *fieldFrame) word(at func([]rune, int) int, end, i int) int {
	if ff.o.Secret {
		return end
	}
	return at(ff.runes, i)
}

func (ff *fieldFrame) spans() []span {
	if !ff.multi {
		return []span{{0, len(ff.runes)}}
	}
	return wrapSpans(ff.runes, ff.st.width, ff.advance)
}

// line is the line the caret is on.
func (ff *fieldFrame) line() span {
	ls := ff.spans()
	return ls[lineOf(ls, ff.st.cursor)]
}

// nearest is the offset on line sp nearest x from the line's start.
func (ff *fieldFrame) nearest(sp span, x float32) int {
	best, bestD := sp.from, float32(math.MaxFloat32)
	for i := sp.from; i <= sp.to; i++ {
		if d := abs32(ff.advance(sp.from, i) - x); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// typable is what of s can be typed here.
func (ff *fieldFrame) typable(s string) []rune {
	var add []rune
	for _, r := range s {
		if (r >= ' ' || ff.multi && r == '\n') && (ff.o.Accept == nil || ff.o.Accept(r)) {
			add = append(add, r)
		}
	}
	return add
}

// replace puts add in place of runes [lo, hi), with the caret after it.
func (ff *fieldFrame) replace(lo, hi int, add []rune) {
	ff.runes = append(ff.runes[:lo:lo], append(add, ff.runes[hi:]...)...)
	ff.st.cursor, ff.st.anchor = lo+len(add), lo+len(add)
	ff.res.Changed = true
}

func (ff *fieldFrame) now() inputEdit { return inputEdit{string(ff.runes), ff.st.cursor, ff.st.anchor} }

func (ff *fieldFrame) restore(e inputEdit) {
	ff.runes, ff.st.cursor, ff.st.anchor, ff.res.Changed = []rune(e.text), e.cursor, e.anchor, true
}

// record keeps before for undo if the text has changed since, unless this edit joins the last.
func (ff *fieldFrame) record(before inputEdit, join string) {
	st := ff.st
	if string(ff.runes) != before.text {
		if join == "" || join != st.join {
			st.undo = append(st.undo, before)
			st.undo = st.undo[max(0, len(st.undo)-undoMax):]
		}
		st.redo = nil
	}
	st.join = join
}

// pointer puts the caret where a press lands, a double press selecting a word and a triple a line; a drag extends
// the selection.
func (ff *fieldFrame) pointer() {
	c, in, st, runes := ff.c, ff.in, ff.st, ff.runes
	if in.Clicked || in.Pressed {
		if r, ok := c.Rect(ff.id); ok {
			at := st.cursor
			if ff.multi {
				if tr, ok := c.Rect(ui.Child(ff.id, "text")); ok {
					ls := ff.spans()
					k := clampInt(int((in.Pointer().Y-tr.Min.Y)/lineH), 0, len(ls)-1)
					at = ff.nearest(ls[k], in.Pointer().X-tr.Min.X)
				}
			} else {
				at = ff.nearest(span{0, len(runes)}, in.Pointer().X-r.Min.X-10+st.scroll)
			}
			fresh := !st.dragging
			switch {
			case fresh && in.Presses == 2 && !ff.o.Secret:
				st.anchor, st.cursor = wordAt(runes, at)
			case fresh && in.Presses >= 2:
				st.anchor, st.cursor = lineAt(runes, at, ff.multi)
			case !st.dragging || st.presses < 2:
				st.cursor = at
				// The press anchors a selection (Shift keeps the old anchor); dragging extends it.
				if fresh && in.Mods&render.ModShift == 0 {
					st.anchor = at
				}
			}
			st.hasGoal = false
			if fresh {
				st.presses = in.Presses
			}
		}
	}
	st.dragging = in.Pressed
	ff.moved = in.Clicked || in.Pressed
}

// outside makes the edits that come from outside the keys: the extra's edit, and files dropped on the field.
func (ff *fieldFrame) outside() {
	o, st := ff.o, ff.st
	if e := ff.x.edit; e != nil && !o.Disabled {
		before := ff.now()
		ff.replace(clampInt(e.From, 0, len(ff.runes)), clampInt(e.To, 0, len(ff.runes)), ff.typable(e.Insert))
		ff.record(before, e.Join)
		ff.moved = true
	}
	if d := ff.in.Dropped; d != nil && o.Files != nil && !o.Disabled {
		if paths, ok := d.Data.([]string); ok && d.Kind == "file" {
			before := ff.now()
			ff.replace(min(st.cursor, st.anchor), max(st.cursor, st.anchor), ff.typable(o.Files(Pasted{Paths: paths})))
			ff.record(before, "")
			ff.moved = true
		}
	}
}

// key edits with one key, as editKey names what it does.
func (ff *fieldFrame) key(k render.KeyEvent) {
	c, o, st := ff.c, ff.o, ff.st
	shift, mods := k.Mods&render.ModShift != 0, k.Mods&^render.ModShift
	move := func(to int) {
		st.cursor = clampInt(to, 0, len(ff.runes))
		if !shift {
			st.anchor = st.cursor
		}
	}
	lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
	cut := func() { ff.replace(lo, hi, nil) }
	insert := func(s string) {
		if add := ff.typable(s); len(add) > 0 {
			ff.replace(lo, hi, add)
		}
	}
	before, join := ff.now(), ""
	key := editKey(k.Key, mods)
	if ff.multi {
		switch {
		case key == "submit" && (shift || !o.EnterSubmits):
			key = "newline"
		case k.Key == "CR" && mods == platform.primary:
			key = "submit"
		}
	}
	if key != "up" && key != "down" {
		st.hasGoal = false
	}
	ff.moved = true
	switch key {
	case "undo", "redo":
		from, to := &st.undo, &st.redo
		if key == "redo" {
			from, to = to, from
		}
		if n := len(*from); n > 0 {
			*to = append(*to, ff.now())
			ff.restore((*from)[n-1])
			*from = (*from)[:n-1]
		}
		st.join = ""
		return
	case "submit":
		ff.res.Submitted = true
	case "newline":
		insert("\n")
	case "cancel":
		ff.res.Cancelled = true
		c.Blur()
	case "tab":
		if o.TabMoves {
			c.FocusNext(map[bool]int{false: 1, true: -1}[shift])
		}
	case "up", "down":
		ls := ff.spans()
		n := lineOf(ls, st.cursor)
		if !st.hasGoal {
			st.goal, st.hasGoal = ff.advance(ls[n].from, st.cursor), true
		}
		switch {
		case key == "up" && n == 0:
			move(0)
		case key == "down" && n == len(ls)-1:
			move(len(ff.runes))
		case key == "up":
			move(ff.nearest(ls[n-1], st.goal))
		default:
			move(ff.nearest(ls[n+1], st.goal))
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
		move(ff.word(wordLeft, 0, st.cursor))
	case "word-right":
		move(ff.word(wordRight, len(ff.runes), st.cursor))
	case "line-start":
		move(ff.line().from)
	case "line-end":
		move(ff.line().to)
	case "text-start":
		move(0)
	case "text-end":
		move(len(ff.runes))
	case "select-all":
		st.anchor, st.cursor = 0, len(ff.runes)
	case "copy", "cut":
		if lo != hi && !o.Secret {
			c.Clipboard().SetClipboard(string(ff.runes[lo:hi]))
			if key == "cut" {
				cut()
			}
		}
	case "paste":
		if s, ok := pasteFiles(c, o); ok {
			insert(s)
		} else if s, ok := c.Clipboard().ReadClipboard(); ok {
			insert(s)
		}
	case "delete-back", "delete-word-back", "delete-line-back":
		if lo == hi {
			switch key {
			case "delete-back":
				lo = max(lo-1, 0)
			case "delete-word-back":
				lo = ff.word(wordLeft, 0, lo)
			case "delete-line-back":
				lo = ff.line().from
			}
		}
		cut()
	case "delete", "delete-word":
		if lo == hi {
			if key == "delete" {
				hi = min(hi+1, len(ff.runes))
			} else {
				hi = ff.word(wordRight, len(ff.runes), hi)
			}
		}
		cut()
	default:
		if mods == 0 && utf8.RuneCountInString(k.Key) == 1 {
			insert(k.Key)
			join = "type"
		}
	}
	ff.record(before, join)
}

// access takes an assistive technology's action: setting the value, selecting, or stepping a number.
func (ff *fieldFrame) access(a ui.AccessAction) {
	st, n := ff.st, len(ff.runes)
	before := ff.now()
	switch a.Action {
	case ui.ActionSetValue:
		ff.replace(0, n, ff.typable(a.Value))
	case ui.ActionSelect:
		st.anchor, st.cursor = clampInt(a.Selection.Start, 0, n), clampInt(a.Selection.End, 0, n)
	case ui.ActionIncrement, ui.ActionDecrement:
		v, err := strconv.ParseFloat(strings.TrimSpace(string(ff.runes)), 64)
		if err != nil || ff.o.Step <= 0 {
			break
		}
		v += ff.o.Step * float64(steps([]ui.AccessAction{a}))
		ff.replace(0, n, ff.typable(strconv.FormatFloat(snap(v, ff.o.Step), 'f', -1, 64)))
	}
	ff.record(before, "")
	ff.moved = true
}

// style is the field's box: its border by state, what it says to assistive technology, and what it takes dropped.
func (ff *fieldFrame) style() ui.Style {
	c, o, st := ff.c, ff.o, ff.st
	width := o.Width
	if width == (ui.Size{}) {
		width = ui.Flex(1)
	}
	border := col(c, tInputBorder)
	switch {
	case o.Invalid:
		border = col(c, tInputInvalid)
	case ff.in.Focused:
		border = col(c, tInputFocus)
	}
	if ff.x.stroke != (render.Color{}) {
		border = ff.x.stroke
	}
	mode := o.KeyMode
	if mode == "" {
		mode = "input"
	}
	actions := textActions
	if o.Step > 0 {
		actions = numberActions
	}
	if o.Disabled {
		actions = nil
	}
	style := ui.Style{W: width, Fill: col(c, tInputBg), Stroke: border, Border: 1, Radius: 6, Clip: true, Focusable: true, DefaultFocus: o.DefaultFocus, Disabled: o.Disabled,
		Cursor: render.CursorText, Help: o.Help, KeyMode: mode, Text: true, Role: ui.RoleTextBox, Label: o.Label, Value: ff.shown(0, len(ff.runes)),
		Selection: ui.TextRange{Start: min(st.cursor, st.anchor), End: max(st.cursor, st.anchor)}, Actions: actions}
	if o.Files != nil && !o.Disabled {
		style.Drop = []string{"file"}
	}
	return style
}

func (ff *fieldFrame) fg() render.Color {
	if ff.o.Disabled {
		return col(ff.c, tDisabledFg)
	}
	return col(ff.c, tInputFg)
}

// drawLines draws a multi-line field: its wrapped lines in a box that scrolls to keep the caret's line in view.
func (ff *fieldFrame) drawLines(key any, style ui.Style) {
	c, o, x, st, runes := ff.c, ff.o, ff.x, ff.st, ff.runes
	if r, ok := c.Rect(ff.id); ok {
		st.width = r.Dx() - 20 - x.padR
	}
	ls := ff.spans()
	rows := clampInt(len(ls), max(o.MinLines, 1), o.Lines)
	if ff.moved {
		// Keep the caret's line in view.
		top, view := float32(lineOf(ls, st.cursor))*lineH, float32(rows)*lineH
		sc := c.Scroll(ff.id)
		if top < sc.Y {
			sc.Y = top
		}
		if top+lineH > sc.Y+view {
			sc.Y = top + lineH - view
		}
		c.ScrollTo(ff.id, sc)
	}
	lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
	focused, fg, font := ff.in.Focused, ff.fg(), ff.font
	style.Dir, style.H, style.Pad, style.ScrollY = ui.Column, ui.Px(float32(rows)*lineH+2*linesPadY), ui.PadXY(10, linesPadY), true
	style.Pad.R += x.padR
	c.Box(key, style, func(rc *ui.Ctx) {
		rc.Paint("text", ui.Flex(1), ui.Px(float32(len(ls))*lineH), func(f render.Frame, r render.Rect) {
			if len(runes) == 0 && o.Placeholder != "" {
				run := c.Shaper().Shape(o.Placeholder, font)
				f.Glyphs(run, render.Point{X: r.Min.X, Y: r.Min.Y + lineH/2 + (run.Ascent-run.Descent)/2}, col(c, tInputHint))
			}
			for k, sp := range ls {
				y := r.Min.Y + float32(k)*lineH
				if a, b := max(lo, sp.from), min(hi, sp.to); focused && hi > lo && a <= b && lo <= sp.to && hi >= sp.from {
					x0, x1 := ff.advance(sp.from, a), ff.advance(sp.from, b)
					if hi > sp.to && sp.to < len(runes) {
						x1 += 4 // the newline, or the wrap, is selected too
					}
					f.Rect(render.XYWH(r.Min.X+x0, y+1, x1-x0, 16), render.Style{Fill: col(c, tInputSelection)})
				}
				// A line is drawn in up to three runs: before, in and after the muted span.
				cuts := []int{sp.from, clampInt(x.muted[0], sp.from, sp.to), clampInt(x.muted[1], sp.from, sp.to), sp.to}
				for i := range 3 {
					a, b := cuts[i], max(cuts[i], cuts[i+1])
					if b <= a {
						continue
					}
					tc := fg
					if i == 1 {
						tc = col(c, theme.FgMuted)
					}
					run := c.Shaper().Shape(string(runes[a:b]), font)
					f.Glyphs(run, render.Point{X: r.Min.X + ff.advance(sp.from, a), Y: y + lineH/2 + (run.Ascent-run.Descent)/2}, tc)
				}
			}
			if focused {
				k := lineOf(ls, st.cursor)
				cx := ff.advance(ls[k].from, st.cursor)
				f.Rect(render.XYWH(r.Min.X+cx, r.Min.Y+float32(k)*lineH+1, 1, 16), render.Style{Fill: col(c, tInputCaret)})
			}
		})
		if x.inlay != nil {
			x.inlay(rc)
		}
	})
}

// drawLine draws a one-line field, scrolled sideways to keep the caret in view.
func (ff *fieldFrame) drawLine(key any, style ui.Style) {
	c, o, st := ff.c, ff.o, ff.st
	if r, ok := c.Rect(ff.id); ok {
		view := r.Dx() - 20
		cx := ff.prefix(st.cursor)
		if cx-st.scroll > view {
			st.scroll = cx - view
		}
		if cx < st.scroll {
			st.scroll = cx
		}
	}
	lo, hi := min(st.cursor, st.anchor), max(st.cursor, st.anchor)
	selFrom, selTo, caret := ff.prefix(lo), ff.prefix(hi), ff.prefix(st.cursor)
	scroll, focused, fg, font := st.scroll, ff.in.Focused, ff.fg(), ff.font
	shown := ff.shown(0, len(ff.runes))
	style.H, style.Pad, style.Cross = ui.Px(30), ui.PadXY(10, 0), ui.Center
	c.Row(key, style, func(rc *ui.Ctx) {
		rc.Paint("text", ui.Flex(1), ui.Px(30), func(f render.Frame, r render.Rect) {
			if focused && hi > lo {
				sel := col(c, tInputSelection)
				f.Rect(render.XYWH(r.Min.X+selFrom-scroll, r.Min.Y+7, selTo-selFrom, 16), render.Style{Fill: sel})
			}
			s, tc := shown, fg
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
	case km{"z", platform.primary}:
		return "undo"
	case km{"Z", platform.primary}:
		return "redo"
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

// wordAt is the word around i: its run of word characters, else the run of spaces there, else the one character.
func wordAt(rs []rune, i int) (int, int) {
	if i == len(rs) && i > 0 {
		i--
	}
	if i >= len(rs) {
		return i, i
	}
	same := func(r rune) bool { return isWord(r) == isWord(rs[i]) && unicode.IsSpace(r) == unicode.IsSpace(rs[i]) }
	if !isWord(rs[i]) && !unicode.IsSpace(rs[i]) {
		return i, i + 1
	}
	lo, hi := i, i
	for lo > 0 && same(rs[lo-1]) {
		lo--
	}
	for hi < len(rs) && same(rs[hi]) {
		hi++
	}
	return lo, hi
}

// lineAt is the line around i, between newlines, its newline included; a single-line field's whole text.
func lineAt(rs []rune, i int, multi bool) (int, int) {
	if !multi {
		return 0, len(rs)
	}
	lo, hi := i, i
	for lo > 0 && rs[lo-1] != '\n' {
		lo--
	}
	for hi < len(rs) && rs[hi] != '\n' {
		hi++
	}
	if hi < len(rs) {
		hi++
	}
	return lo, hi
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
