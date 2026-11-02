package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

var tSelection = theme.Declare("text.selection", "alpha(accent, 28%)")

func init() {
	for _, on := range []string{"bg.base", "bg.surface", "bg.raised"} {
		theme.Readable("text.selection", on, 1.5)
	}
}

// textSel is the window's text selection: a run of text inside one Selectable box, from the press to the pointer,
// across as many text nodes as it passes, in the order they were built.
type textSel struct {
	scope         ID
	anchor, focus selPos
	held          bool
	clicks        int
	order         map[ID]int // this frame's text nodes in scope, in the order they were built
	texts         []*elem
	anchorAt      int // the anchor's place in that order when last built: which edge it holds at once scrolled away
}

// selPos is a place in the text: a text node, and a byte offset into what it shows.
type selPos struct {
	id  ID
	off int
}

// selScope is the Selectable box whose text a press at p selects, or 0 when the press is on a control in it (anything
// clickable or focusable: a button, an input, a list; a drag source) or outside any.
func (u *UI) selScope(p render.Point) ID {
	hits := u.Hit(p)
	if len(hits) == 0 {
		return 0
	}
	for id := hits[0]; id != 0; id = u.parents[id] {
		e, ok := u.byID[id]
		if !ok {
			return 0
		}
		s := e.style
		if s.Interactive || s.Focusable || s.Drag != nil {
			return 0
		}
		if s.Selectable {
			return id
		}
	}
	return 0
}

// texts are the text nodes under scope, in the order they were built.
func (u *UI) texts(scope ID) []*elem {
	root, ok := u.byID[scope]
	if !ok {
		return nil
	}
	var out []*elem
	var walk func(e *elem)
	walk = func(e *elem) {
		if e.kind == kText && e.run != nil && e.run.Text != "" {
			out = append(out, e)
		}
		for _, k := range e.kids {
			walk(k)
		}
	}
	walk(root)
	return out
}

// selAt is the place in scope's text nearest p: on the line p is level with, or the nearest line, at the character
// boundary nearest p.
func (u *UI) selAt(texts []*elem, p render.Point) (selPos, bool) {
	var best *elem
	bestDY, bestDX := float32(-1), float32(0)
	for _, e := range texts {
		r := e.rect()
		dy := max(r.Min.Y-p.Y, p.Y-r.Max.Y, 0)
		dx := max(r.Min.X-p.X, p.X-r.Max.X, 0)
		if best == nil || dy < bestDY || dy == bestDY && dx < bestDX {
			best, bestDY, bestDX = e, dy, dx
		}
	}
	if best == nil {
		return selPos{}, false
	}
	return selPos{best.id, u.offsetAt(best, p.X-best.rect().Min.X)}, true
}

// offsetAt is the byte offset of the cluster boundary in e's text nearest x across it.
func (u *UI) offsetAt(e *elem, x float32) int {
	best, bestD := 0, float32(-1)
	for b, cx := range e.run.Carets() {
		d := cx - x
		if d < 0 {
			d = -d
		}
		if bestD < 0 || d < bestD {
			best, bestD = b, d
		}
	}
	return best
}

// selPress starts a selection at a primary press on selectable text, extending it with Shift, or selecting the word
// or the line on a double or triple press. A press anywhere else ends the selection.
func (u *UI) selPress(ev render.PointerEvent) {
	scope := u.selScope(ev.Pos)
	u.selLast = scope
	if scope == 0 {
		u.sel = nil
		return
	}
	texts := u.texts(scope)
	at, ok := u.selAt(texts, ev.Pos)
	if !ok {
		u.sel = nil
		return
	}
	if s := u.sel; s != nil && s.scope == scope && ev.Mods&render.ModShift != 0 {
		s.focus, s.held = at, true
		return
	}
	clicks := u.in.presses
	if s := u.sel; s == nil || s.scope != scope {
		clicks = 1
	}
	s := &textSel{scope: scope, anchor: at, focus: at, held: true, clicks: clicks}
	u.sel = s
	e := u.byID[at.id]
	switch {
	case clicks == 2:
		s.anchor.off, s.focus.off = wordAround(e.run.Text, at.off)
	case clicks >= 3:
		s.anchor.off, s.focus.off = 0, len(e.run.Text)
	}
}

// selMove takes the selection's end to the pointer while the press holds.
func (u *UI) selMove(p render.Point) {
	s := u.sel
	if s == nil || !s.held || s.clicks > 1 {
		return
	}
	if at, ok := u.selAt(u.texts(s.scope), p); ok {
		s.focus = at
	}
}

// wordAround is the word, by letters, digits and _, around byte offset off of s; or the character there.
func wordAround(s string, off int) (int, int) {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	lo, hi := off, off
	for lo > 0 {
		r, n := utf8.DecodeLastRuneInString(s[:lo])
		if !word(r) {
			break
		}
		lo -= n
	}
	for hi < len(s) {
		r, n := utf8.DecodeRuneInString(s[hi:])
		if !word(r) {
			break
		}
		hi += n
	}
	if lo == hi && hi < len(s) {
		_, n := utf8.DecodeRuneInString(s[hi:])
		hi += n
	}
	return lo, hi
}

// selFrame orders this frame's text in the selection's scope, dropping the selection once its scope or ends are gone.
func (u *UI) selFrame() {
	s := u.sel
	if s == nil {
		return
	}
	s.texts = u.texts(s.scope)
	s.order = make(map[ID]int, len(s.texts))
	for i, e := range s.texts {
		s.order[e.id] = i
	}
	ai, a := s.order[s.anchor.id]
	_, f := s.order[s.focus.id]
	switch {
	case !f || len(s.texts) == 0:
		u.sel = nil
	case a:
		s.anchorAt = ai
	case s.anchorAt <= s.order[s.focus.id]:
		// The anchor scrolled out of a list that builds only what shows: it holds at the edge it went past.
		s.anchor, s.anchorAt = selPos{s.texts[0].id, 0}, 0
	default:
		last := s.texts[len(s.texts)-1]
		s.anchor, s.anchorAt = selPos{last.id, len(last.run.Text)}, len(s.texts)-1
	}
}

// selAll selects all the text in the Selectable box last pressed in.
func (u *UI) selAll() bool {
	texts := u.texts(u.selLast)
	if len(texts) == 0 {
		return false
	}
	last := texts[len(texts)-1]
	u.sel = &textSel{scope: u.selLast, anchor: selPos{texts[0].id, 0}, focus: selPos{last.id, len(last.run.Text)}}
	u.selFrame()
	return true
}

// selScroll scrolls the box the selection is in while a drag holds past its top or bottom edge, faster the further
// out the pointer is, and takes the selection's end along.
func (u *UI) selScroll() {
	s := u.sel
	if s == nil || !s.held || s.clicks > 1 {
		return
	}
	box := ID(0)
	for id := s.scope; id != 0; id = u.parents[id] {
		if u.flags[id].scrollY {
			box = id
			break
		}
	}
	r, ok := u.Rect(box)
	if box == 0 || !ok {
		return
	}
	p := u.in.pos
	var dy float32
	switch {
	case p.Y < r.Min.Y:
		dy = -min((r.Min.Y-p.Y)/2+4, 60)
	case p.Y > r.Max.Y:
		dy = min((p.Y-r.Max.Y)/2+4, 60)
	default:
		return
	}
	u.ScrollBy(box, 0, dy)
	u.selMove(render.Point{X: p.X, Y: min(max(p.Y, r.Min.Y+1), r.Max.Y-1)})
	u.wakeAt(u.now().Add(16 * time.Millisecond))
}

// selRange is the selection's ends in order, while it covers some text.
func (u *UI) selRange() (from, to selPos, ok bool) {
	s := u.sel
	if s == nil || s.order == nil || s.anchor == s.focus {
		return selPos{}, selPos{}, false
	}
	from, to = s.anchor, s.focus
	if fi, ti := s.order[from.id], s.order[to.id]; ti < fi || ti == fi && to.off < from.off {
		from, to = to, from
	}
	return from, to, true
}

// selSpan is the part of text node e the selection covers, by byte offsets.
func (u *UI) selSpan(e *elem) (int, int, bool) {
	from, to, ok := u.selRange()
	if !ok {
		return 0, 0, false
	}
	i, in := u.sel.order[e.id]
	fi, ti := u.sel.order[from.id], u.sel.order[to.id]
	if !in || i < fi || i > ti {
		return 0, 0, false
	}
	lo, hi := 0, len(e.run.Text)
	if i == fi {
		lo = min(from.off, hi)
	}
	if i == ti {
		hi = min(to.off, hi)
	}
	return lo, hi, hi > lo
}

// paintSel draws the selection under text node e, drawn at r.
func (u *UI) paintSel(f render.Frame, e *elem, r render.Rect) {
	lo, hi, ok := u.selSpan(e)
	if !ok || u.theme == nil {
		return
	}
	x0, x1 := e.run.CaretX(lo), e.run.CaretX(hi)
	f.Rect(render.Rect{Min: render.Point{X: r.Min.X + x0, Y: r.Min.Y}, Max: render.Point{X: r.Min.X + x1, Y: r.Max.Y}},
		render.Style{Fill: u.theme.C(tSelection)})
}

// SelectedText is the text selected, nodes on one line joined by what lies between them (nothing, or a space) and
// lines by newlines; "" for none.
func (u *UI) SelectedText() string {
	if _, _, ok := u.selRange(); !ok {
		return ""
	}
	var b strings.Builder
	var prev *elem
	for _, e := range u.sel.texts {
		lo, hi, ok := u.selSpan(e)
		if !ok {
			continue
		}
		if prev != nil {
			pr, r := prev.rect(), e.rect()
			switch {
			case r.Min.Y >= pr.Max.Y-1:
				b.WriteByte('\n')
			case r.Min.X > pr.Max.X+1:
				b.WriteByte(' ')
			}
		}
		b.WriteString(e.run.Text[lo:hi])
		prev = e
	}
	return b.String()
}

// selKey is the selection's keys: ⌘A (and ⌃A) selects all the text in the box last pressed in, while what has
// focus is that box or holds it; ⌘C or ⌃⇧C copies.
func (u *UI) selKey(ev render.KeyEvent) bool {
	if strings.EqualFold(ev.Key, "a") && (ev.Mods == render.ModSuper || ev.Mods == render.ModCtrl) && u.selLast != 0 && u.holds(u.in.focus, u.selLast) {
		return u.selAll()
	}
	return u.copyKey(ev)
}

// holds says node a is b or one of b's ancestors.
func (u *UI) holds(a, b ID) bool {
	for id := b; id != 0; id = u.parents[id] {
		if id == a {
			return true
		}
	}
	return a == 0
}

// copyKey copies the selection on the platform's copy key (⌘C, or ⌃⇧C), saying whether it did.
func (u *UI) copyKey(ev render.KeyEvent) bool {
	if !strings.EqualFold(ev.Key, "c") || ev.Mods != render.ModSuper && ev.Mods != render.ModCtrl|render.ModShift {
		return false
	}
	text := u.SelectedText()
	if text == "" || u.clip == nil {
		return false
	}
	u.clip.SetClipboard(text)
	return true
}
