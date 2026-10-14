package vim

import (
	"bytes"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// Multi-cursor (ED-8), after vim-visual-multi: ⌃n selects the word under the cursor and then each next whole-word
// match, ⌃↓ and ⌃↑ add a cursor below or above. With regions selected (extend mode) c, s, d, x, I, A and o act on
// all of them; with cursors, any normal-mode command runs at each one, and insert mode types at all of them. A
// command and the typing after it undo as one step. ⇥ switches between regions and cursors, q skips a match, Q
// drops the current region, Esc ends it.

// Region is one cursor: Head is the cursor, and in extend mode the region runs from Anchor to Head, both included.
type Region struct{ Anchor, Head Pos }

// MultiState is the cursors while there's more than one.
type MultiState struct {
	Regions []Region // in buffer order
	Primary int      // the one ⌃n added last, which the window follows
	Extend  bool     // regions rather than cursors
}

type multi struct {
	regs      []mregion
	primary   int
	extend    bool
	word      string
	keys      []string // the command at the cursors so far
	group     text.UndoGroup
	inserting bool
}

type mregion struct{ anchor, head text.AnchorID }

// Multi is the cursors, if there's more than the one.
func (e *Engine) Multi() (MultiState, bool) {
	m := e.multi
	if m == nil {
		return MultiState{}, false
	}
	v := e.view()
	st := MultiState{Primary: m.primary, Extend: m.extend}
	for _, r := range m.regs {
		st.Regions = append(st.Regions, Region{Anchor: v.pos(e.resolve(r.anchor)), Head: v.pos(e.resolve(r.head))})
	}
	return st, true
}

func (e *Engine) resolve(a text.AnchorID) int {
	off, _ := e.ed.Resolve(a)
	return min(off, e.ed.Buf().Len())
}

func (e *Engine) anchor(off int) text.AnchorID { return e.ed.Anchor(off, text.Right) }

func isWordByte(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordAt is the word the offset is in, as [start, end).
func wordAt(b []byte, off int) (int, int, bool) {
	if off >= len(b) {
		return 0, 0, false
	}
	if r, _ := utf8.DecodeRune(b[off:]); !isWordByte(r) {
		return 0, 0, false
	}
	start, end := off, off
	for start > 0 {
		r, n := utf8.DecodeLastRune(b[:start])
		if !isWordByte(r) {
			break
		}
		start -= n
	}
	for end < len(b) {
		r, n := utf8.DecodeRune(b[end:])
		if !isWordByte(r) {
			break
		}
		end += n
	}
	return start, end, true
}

// wholeWordAfter is the next whole-word match of w at or after from, wrapping round.
func wholeWordAfter(b []byte, w string, from int) (int, bool) {
	find := func(lo, hi int) (int, bool) {
		for i := lo; i < hi; {
			j := bytes.Index(b[i:hi], []byte(w))
			if j < 0 {
				return 0, false
			}
			at := i + j
			s, e, ok := wordAt(b, at)
			if ok && s == at && e == at+len(w) {
				return at, true
			}
			i = at + 1
		}
		return 0, false
	}
	if at, ok := find(from, len(b)); ok {
		return at, true
	}
	return find(0, min(from, len(b)))
}

// MultiNext is ⌃n: the word under the cursor as a region, then the next whole-word match of it as another.
func (e *Engine) MultiNext() {
	b := e.ed.Buf().Bytes()
	v := e.view()
	m := e.multi
	if m == nil || m.word == "" {
		e.multiEnd()
		s, end, ok := wordAt(b, v.offset(e.cur))
		if !ok {
			return
		}
		e.multi = &multi{extend: true, word: string(b[s:end])}
		e.addRegion(s, end-lastRuneLen(b[:end]))
		return
	}
	e.addNextMatch(e.resolve(m.regs[m.primary].head) + 1)
}

// addNextMatch adds the next whole-word match at or after from as a region.
func (e *Engine) addNextMatch(from int) {
	m := e.multi
	b := e.ed.Buf().Bytes()
	at, ok := wholeWordAfter(b, m.word, from)
	if !ok {
		return
	}
	for _, r := range m.regs {
		if e.resolve(r.anchor) == at {
			return // every match has a region
		}
	}
	e.addRegion(at, at+len(m.word)-lastRuneLen([]byte(m.word)))
}

func lastRuneLen(b []byte) int {
	_, n := utf8.DecodeLastRune(b)
	return max(n, 1)
}

// addRegion adds a region from anchor to head and makes it the primary; the cursor goes to it.
func (e *Engine) addRegion(anchor, head int) {
	m := e.multi
	m.regs = append(m.regs, mregion{e.anchor(anchor), e.anchor(head)})
	e.sortRegions(len(m.regs) - 1)
	e.visual = nil
	e.cur = e.view().pos(head)
	e.sync()
}

// sortRegions keeps the regions in buffer order, the primary following region i.
func (e *Engine) sortRegions(i int) {
	m := e.multi
	p := m.regs[i]
	slices.SortStableFunc(m.regs, func(a, b mregion) int { return e.resolve(a.head) - e.resolve(b.head) })
	m.primary = slices.Index(m.regs, p)
}

// MultiVertical is ⌃↓ (dir 1) and ⌃↑ (-1): a cursor on the next line past the last one that way, at the same column.
func (e *Engine) MultiVertical(dir int) {
	v := e.view()
	if e.multi == nil {
		e.multi = &multi{}
		e.multi.regs = []mregion{{e.anchor(v.offset(e.cur)), e.anchor(v.offset(e.cur))}}
	}
	m := e.multi
	if m.extend {
		e.collapse()
	}
	edge := m.regs[0]
	if dir > 0 {
		edge = m.regs[len(m.regs)-1]
	}
	p := v.pos(e.resolve(edge.head))
	line := p.Line + dir
	if line < 0 || line >= v.lines() {
		return
	}
	col := v.colAt(line, v.vcol(p.Line, p.Col), false)
	off := v.offset(Pos{Line: line, Col: col})
	e.addRegion(off, off)
}

// collapse turns regions into cursors at their heads.
func (e *Engine) collapse() {
	m := e.multi
	for i, r := range m.regs {
		e.ed.Release(r.anchor)
		m.regs[i].anchor = e.anchor(e.resolve(r.head))
	}
	m.extend = false
}

func (e *Engine) multiEnd() {
	m := e.multi
	if m == nil {
		return
	}
	for _, r := range m.regs {
		e.ed.Release(r.anchor)
		e.ed.Release(r.head)
	}
	if m.group != 0 && e.outerGroup == m.group {
		e.outerGroup = 0
	}
	e.multi = nil
	if e.mode == Insert {
		e.mode = Normal
		e.ins = nil
	}
}

// multiKey handles a key while there are several cursors, and reports whether it did.
func (e *Engine) multiKey(k string) bool {
	m := e.multi
	if m.inserting {
		return e.multiInsertKey(k)
	}
	if len(m.keys) == 0 {
		switch k {
		case "<Esc>":
			e.multiEnd()
			return true
		case "<Tab>":
			if m.extend {
				e.collapse()
			} else {
				m.extend = true
			}
			return true
		case "Q":
			e.dropPrimary()
			return true
		case "q":
			if m.extend && m.word != "" {
				skipped := e.resolve(m.regs[m.primary].head)
				e.dropPrimary()
				if e.multi != nil {
					e.addNextMatch(skipped + 1)
				}
			}
			return true
		case "u", "<C-r>", ":":
			e.multiEnd()
			return false
		}
		if m.extend {
			switch k {
			case "c", "s":
				e.deleteRegions()
				e.multiStartInsert()
				return true
			case "d", "x":
				e.deleteRegions()
				return true
			case "I", "A":
				e.cursorsAtEnds(k == "A")
				e.multiStartInsert()
				return true
			case "o":
				for i, r := range m.regs {
					m.regs[i].anchor, m.regs[i].head = r.head, r.anchor
				}
				return true
			}
			e.collapse()
		}
	}
	return e.multiCommand(k)
}

// dropPrimary removes the current region; with one left, it's the only cursor.
func (e *Engine) dropPrimary() {
	m := e.multi
	r := m.regs[m.primary]
	e.ed.Release(r.anchor)
	e.ed.Release(r.head)
	m.regs = slices.Delete(m.regs, m.primary, m.primary+1)
	if len(m.regs) == 0 {
		e.multi = nil
		return
	}
	m.primary = min(m.primary, len(m.regs)-1)
	e.cur = e.view().pos(e.resolve(m.regs[m.primary].head))
	e.sync()
}

// regionRanges are the regions' text, inclusive of the head's character, in buffer order.
func (e *Engine) regionRanges() []text.Range {
	b := e.ed.Buf().Bytes()
	var out []text.Range
	for _, r := range e.multi.regs {
		a, h := e.resolve(r.anchor), e.resolve(r.head)
		s, end := min(a, h), max(a, h)
		if end < len(b) {
			_, n := utf8.DecodeRune(b[end:])
			end += n
		}
		if len(out) > 0 && s < out[len(out)-1].End {
			s = out[len(out)-1].End
		}
		if end > s {
			out = append(out, text.Range{Start: s, End: end})
		}
	}
	return out
}

func (e *Engine) deleteRegions() {
	m := e.multi
	if m.group == 0 {
		m.group = e.ed.NewGroup()
	}
	var rs []text.Replace
	for _, r := range e.regionRanges() {
		rs = append(rs, text.Replace{Range: r})
	}
	if len(rs) > 0 {
		e.do(m.group, rs...)
	}
	e.collapse()
	e.follow()
}

// cursorsAtEnds puts each cursor at its region's start, or just past its end.
func (e *Engine) cursorsAtEnds(after bool) {
	m := e.multi
	rs := e.regionRanges()
	for i, r := range m.regs {
		e.ed.Release(r.anchor)
		e.ed.Release(r.head)
		off := rs[min(i, len(rs)-1)].Start
		if after {
			off = rs[min(i, len(rs)-1)].End
		}
		m.regs[i] = mregion{e.anchor(off), e.anchor(off)}
	}
	m.extend = false
}

func (e *Engine) multiStartInsert() {
	m := e.multi
	if m.group == 0 {
		m.group = e.ed.NewGroup()
	}
	m.inserting = true
	e.mode = Insert
	e.follow()
}

// follow puts the engine's cursor on the primary's.
func (e *Engine) follow() {
	m := e.multi
	e.cur = e.view().pos(e.resolve(m.regs[m.primary].head))
	e.sync()
}

// multiCommand runs a normal-mode command at the primary cursor, then, once it's whole, at every other one, all in
// one undo step.
func (e *Engine) multiCommand(k string) bool {
	m := e.multi
	if len(m.keys) == 0 {
		m.group = e.ed.NewGroup()
	}
	m.keys = append(m.keys, k)
	e.outerGroup = m.group
	e.follow()
	e.mode = Normal
	e.keyNow(k)
	if len(e.pend) > 0 || e.cmdline != nil {
		return true // the command isn't whole yet
	}
	keys := m.keys
	m.keys = nil
	v := e.view()
	m.regs[m.primary].head, m.regs[m.primary].anchor = e.reanchor(m.regs[m.primary], v.offset(e.cur))
	inserting := e.mode == Insert
	for i := range m.regs {
		if i == m.primary {
			continue
		}
		e.mode, e.visual = Normal, nil
		e.cur = e.view().pos(e.resolve(m.regs[i].head))
		e.clamp(e.view())
		for _, key := range keys {
			e.keyNow(key)
		}
		e.pend = nil
		m.regs[i].head, m.regs[i].anchor = e.reanchor(m.regs[i], e.view().offset(e.cur))
	}
	if inserting {
		m.inserting = true
		e.mode = Insert
	} else {
		e.mode = Normal
		e.outerGroup = 0
		m.group = 0
	}
	e.dedupe()
	e.follow()
	return true
}

// reanchor moves a cursor's anchors to off.
func (e *Engine) reanchor(r mregion, off int) (head, anchor text.AnchorID) {
	e.ed.Release(r.head)
	e.ed.Release(r.anchor)
	return e.anchor(off), e.anchor(off)
}

// dedupe drops cursors that ended up on the same place.
func (e *Engine) dedupe() {
	m := e.multi
	p := m.regs[m.primary]
	seen := map[int]bool{}
	var out []mregion
	for _, r := range m.regs {
		off := e.resolve(r.head)
		if seen[off] && r != p {
			e.ed.Release(r.head)
			e.ed.Release(r.anchor)
			continue
		}
		seen[off] = true
		out = append(out, r)
	}
	m.regs = out
	m.primary = max(slices.Index(out, p), 0)
}

// keyNow runs a key as the engine would, without the cursors seeing it.
func (e *Engine) keyNow(k string) {
	m := e.multi
	e.multi = nil
	defer func() { e.multi = m }()
	e.Key(k)
}

// multiInsertKey types at every cursor: text, ⌫ and ⏎ go to all of them; Esc goes back to normal mode, each cursor
// stepping back onto the character before it, as vim's does.
func (e *Engine) multiInsertKey(k string) bool {
	m := e.multi
	b := e.ed.Buf().Bytes()
	var rs []text.Replace
	switch k {
	case "<Esc>", "<C-c>":
		v := e.view()
		for i, r := range m.regs {
			p := v.pos(e.resolve(r.head))
			if p.Col > 0 {
				p.Col = v.prevCol(p.Line, p.Col)
			}
			m.regs[i].head, m.regs[i].anchor = e.reanchor(r, v.offset(p))
		}
		m.inserting = false
		e.mode, e.ins = Normal, nil
		if e.outerGroup == m.group {
			e.outerGroup = 0
		}
		m.group = 0
		e.dedupe()
		e.follow()
		return true
	case "<BS>":
		for _, r := range m.regs {
			off := e.resolve(r.head)
			if off > 0 {
				_, n := utf8.DecodeLastRune(b[:off])
				rs = append(rs, text.Replace{Range: text.Range{Start: off - n, End: off}})
			}
		}
	default:
		t, ok := typed(k)
		if !ok {
			e.multiEnd()
			return false
		}
		for _, r := range m.regs {
			off := e.resolve(r.head)
			rs = append(rs, text.Replace{Range: text.Range{Start: off, End: off}, Text: []byte(t)})
		}
	}
	slices.SortFunc(rs, func(a, b text.Replace) int { return a.Range.Start - b.Range.Start })
	rs = slices.CompactFunc(rs, func(a, b text.Replace) bool { return a.Range == b.Range })
	if len(rs) > 0 {
		e.do(m.group, rs...)
	}
	e.follow()
	return true
}

// typed is the text a key types in insert mode, if it types any.
func typed(k string) (string, bool) {
	switch k {
	case "<CR>", "<NL>":
		return "\n", true
	case "<Tab>":
		return "\t", true
	case "<Space>":
		return " ", true
	case "<lt>":
		return "<", true
	}
	if utf8.RuneCountInString(k) == 1 {
		return k, true
	}
	return "", false
}
